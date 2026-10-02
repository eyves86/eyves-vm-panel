<?php
/**
 * 生命周期测试：用 WHMCS 桩驱动真实模块函数，打到真面板。
 * 用法：php tests/lifecycle.php [host] [port]
 */
require_once __DIR__ . '/whmcs_stub.php';

use WHMCS\Module\Server\EyvesCloud\StubStore;

$host = isset($argv[1]) ? $argv[1] : '127.0.0.1';
$port = isset($argv[2]) ? (int) $argv[2] : 8998;

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

StubStore::reset();
StubStore::$tables['tblservers'][] = [
    'id' => 7, 'type' => 'eyvescloud', 'name' => '本地测试面板',
    'hostname' => $host, 'ipaddress' => $host, 'port' => $port, 'secure' => 0,
    'username' => 'admin', 'password' => encrypt('Adapter#2026'), 'accesshash' => '{}',
];
StubStore::$tables['tblproducts'][] = ['id' => 31, 'name' => 'LXC 入门型'];
StubStore::$tables['tblproducts'][] = ['id' => 32, 'name' => 'KVM 标准型'];
// 自定义字段 hostid（每个产品一份，模拟 WHMCS 真实结构）
StubStore::$tables['tblcustomfields'][] = ['id' => 501, 'relid' => 31, 'fieldname' => 'hostid', 'fieldtype' => 'text'];
StubStore::$tables['tblcustomfields'][] = ['id' => 502, 'relid' => 32, 'fieldname' => 'hostid', 'fieldtype' => 'text'];
StubStore::$tables['tblhosting'][] = ['id' => 900, 'userid' => 1, 'packageid' => 31, 'server' => 7, 'domain' => 'lxc-900.local', 'password' => encrypt('init-pw'), 'domainstatus' => 'Active'];
StubStore::$tables['tblhosting'][] = ['id' => 901, 'userid' => 1, 'packageid' => 32, 'server' => 7, 'domain' => 'kvm-901.local', 'password' => encrypt('init-pw'), 'domainstatus' => 'Active'];

require_once __DIR__ . '/../eyvescloud.php';

echo "=== 生命周期测试（WHMCS 桩 + 真面板）===\n\n";

/* ---------- 元数据与配置项 ---------- */
echo "-- 模块元数据 --\n";
$meta = eyvescloud_MetaData();
check('MetaData 有 DisplayName', !empty($meta['DisplayName']));
$cfg = eyvescloud_ConfigOptions();
check('配置项含 runtime', isset($cfg['runtime']) && $cfg['runtime']['Type'] === 'dropdown');
check('配置项含 image', isset($cfg['image']));
check('image 选项已按运行时归类', strpos(implode('', array_keys($cfg['image']['Options'] ?? [])), 'LXC 镜像') !== false, '类型=' . $cfg['image']['Type']);
// v1 把 KVM 镜像门控在 hostKVMAvailable()（需要 /dev/kvm + virsh）之后，
// 沙箱没有 KVM 能力，因此「KVM 镜像也在选项里」要按环境判断。
$kvmImgs = (new \WHMCS\Module\Server\EyvesCloud\EyvesCloud(['serverid' => 7]))->images('kvm');
$hasKvmImages = count($kvmImgs) > 0;
check('KVM 镜像选项与环境一致', $hasKvmImages
    ? strpos(implode('', array_keys($cfg['image']['Options'] ?? [])), 'KVM 镜像') !== false
    : strpos(implode('', array_keys($cfg['image']['Options'] ?? [])), 'KVM 镜像') === false,
    '宿主 KVM 可用=' . ($hasKvmImages ? 'Y' : 'N'));
$optVals = array_values($cfg['image']['Options'] ?? []);
check('LXC 镜像已进入选项', in_array('ubuntu-noble', $optVals), json_encode(array_slice($optVals, 0, 3)));

/* ---------- 连接测试 ---------- */
echo "\n-- 连接 --\n";
$r = eyvescloud_TestConnection(['serverid' => 7]);
check('TestConnection 成功', !empty($r['success']), json_encode($r));
$bad = eyvescloud_TestConnection(['serverid' => 999]);
check('服务器不存在时返回失败而非崩溃', empty($bad['success']));

/* ---------- 规格解析（LXC/KVM 分流核心） ---------- */
echo "\n-- 规格解析 --\n";
$lxcSpec = eyvescloud_spec_from_params([
    'serviceid' => 900, 'pid' => 31, 'domain' => 'lxc-900.local', 'password' => 'pw123',
    'configoptions' => ['runtime' => 'lxc', 'image' => 'ubuntu-noble', 'vcpu' => '1', 'memory_mb' => '512', 'disk_gb' => '10'],
], 'lxc-900-local');
check('LXC 规格 runtime=lxc', $lxcSpec['runtime'] === 'lxc');
check('LXC 规格 镜像正确', $lxcSpec['template_id'] === 'ubuntu-noble');
check('LXC 规格 vcpu 为 float', is_float($lxcSpec['vcpu']) && $lxcSpec['vcpu'] == 1.0);
check('LXC 规格 memory 为 int', is_int($lxcSpec['memory_mb']) && $lxcSpec['memory_mb'] === 512);
check('LXC 规格带 auth 密码', isset($lxcSpec['auth']) && $lxcSpec['auth']['mode'] === 'password');
check('LXC 规格名字已消毒', $lxcSpec['name'] === 'lxc-900-local');

$kvmSpec = eyvescloud_spec_from_params([
    'serviceid' => 901, 'pid' => 32,
    'configoptions' => ['runtime' => 'KVM（完整虚拟化）', 'image' => 'ubuntu-jammy', 'vcpu' => '2', 'memory_mb' => '2048', 'disk_gb' => '40', 'data_disk_gb' => '100', 'nat_ports' => '5', 'firewall' => 'on'],
], 'kvm-901');
check('KVM 规格 runtime=kvm（带说明文字也能识别）', $kvmSpec['runtime'] === 'kvm');
check('KVM 规格 数据盘保留', $kvmSpec['data_disk_gb'] == 100.0);
check('KVM 规格 防火墙布尔化', ($kvmSpec['firewall_enabled'] ?? null) === true);
check('KVM 规格 NAT 端口数', ($kvmSpec['nat_ports'] ?? null) === 5);

$garbage = eyvescloud_spec_from_params(['serviceid' => 1, 'configoptions' => ['runtime' => 'XenServer']]);
check('未知 runtime 落回 lxc', $garbage['runtime'] === 'lxc');

/* ---------- 绑定既有实例（跳过创建，面沙箱无 LXC/KVM 内核支持） ---------- */
echo "\n-- 实例绑定与读写 --\n";
$api = new \WHMCS\Module\Server\EyvesCloud\EyvesCloud(['serverid' => 7]);
$all = $api->instances();
$lxcId = $kvmId = '';
foreach ($all as $it) {
    if ($it['name'] === 'ct-lxc-01') { $lxcId = (string) $it['id']; }
    if ($it['name'] === 'ct-kvm-01') { $kvmId = (string) $it['id']; }
}
check('面板可发现测试实例', $lxcId !== '' && $kvmId !== '', "lxc=$lxcId kvm=$kvmId");
eyvescloud_set_hostid(900, $lxcId, 31);
eyvescloud_set_hostid(901, $kvmId, 32);
check('hostid 写入 LXC 服务', eyvescloud_get_hostid(900, 31) === $lxcId);
check('hostid 写入 KVM 服务', eyvescloud_get_hostid(901, 32) === $kvmId);
check('未绑定的服务读回空', eyvescloud_get_hostid(999, 31) === '');

/* ---------- 幂等：CreateAccount 对已绑定服务应直接 success ---------- */
echo "\n-- 幂等性 --\n";
$params900 = [
    'serviceid' => 900, 'pid' => 31, 'serverid' => 7, 'domain' => 'lxc-900.local',
    'password' => 'init-pw', 'configoptions' => ['runtime' => 'lxc', 'image' => 'ubuntu-noble', 'vcpu' => 1, 'memory_mb' => 512, 'disk_gb' => 10],
];
check('CreateAccount 已绑定 → success（不重建）', eyvescloud_CreateAccount($params900) === 'success');

/* ---------- 防呆：镜像与 runtime 不匹配必须拒绝 ---------- */
echo "\n-- 镜像/runtime 防呆 --\n";
eyvescloud_set_hostid(900, '', 31);
$mismatch = $params900;
$mismatch['configoptions']['runtime'] = 'kvm';   // 产品说是 KVM
$mismatch['configoptions']['image'] = 'ubuntu-noble'; // 但选了 LXC 镜像
$res = eyvescloud_CreateAccount($mismatch);
// 宿主机没有 KVM 能力时，会先被「该运行时没有任何可用镜像」拦下 —— 两种拒绝都正确
check('KVM 产品配 LXC 镜像被拒',
    strpos($res, '不属于') !== false || strpos($res, '没有任何可用镜像') !== false, $res);
eyvescloud_set_hostid(900, $lxcId, 31);

/* ---------- 管理区标签页 ---------- */
echo "\n-- 管理区展示 --\n";
$tab = eyvescloud_AdminServicesTabFields($params900);
check('管理区返回运行时', isset($tab['运行时']) && $tab['运行时'] === 'LXC', json_encode(array_keys($tab)));
check('管理区返回规格', isset($tab['规格']) && strpos($tab['规格'], '512') !== false, $tab['规格'] ?? '');
check('管理区返回流量', isset($tab['流量']));
$params901 = $params900; $params901['serviceid'] = 901; $params901['pid'] = 32;
$tabK = eyvescloud_AdminServicesTabFields($params901);
check('KVM 管理区运行时=KVM', ($tabK['运行时'] ?? '') === 'KVM');

/* ---------- 客户区 ---------- */
echo "\n-- 客户区 --\n";
$ca = eyvescloud_ClientArea($params900);
check('LXC 客户区用 clientarea 模板', ($ca['templatefile'] ?? '') === 'templates/clientarea', json_encode($ca));
check('LXC 客户区 caps 无 VNC', ($ca['vars']['caps']['console_vnc'] ?? true) === false);
check('LXC 客户区 console.type=ssh', ($ca['vars']['console']['type'] ?? '') === 'ssh');
$caK = eyvescloud_ClientArea($params901);
check('KVM 客户区 caps 有 VNC', ($caK['vars']['caps']['console_vnc'] ?? false) === true);
check('KVM 客户区 console.type=vnc', ($caK['vars']['console']['type'] ?? '') === 'vnc');
check('客户区带 assets 路径', isset($ca['vars']['assets']));

/* ---------- 单点登录（控制台） ---------- */
echo "\n-- 控制台分流（SSO） --\n";
$ssoL = eyvescloud_ServiceSingleSignOn($params900);
// 沙箱内 LXC 实例无法真运行，面板会以 412 拒绝——断言「结构化失败而非崩溃」
check('LXC SSO 返回 SSH 控制台或明确未运行', (!empty($ssoL['success']) && strpos((string) ($ssoL['redirectTo'] ?? ''), 'ssh') !== false)
    || strpos((string) ($ssoL['errorMsg'] ?? ''), '未运行') !== false, json_encode($ssoL, JSON_UNESCAPED_UNICODE));

// 前置：把 KVM 实例显式挂起，验证模块是否把面板的挂起策略如实透传给客户。
// 不依赖外部遗留状态，测试自带夹具。
try { $api->power($kvmId, 'suspend'); } catch (Throwable $e) {}
usleep(900000);
$ssoSusp = eyvescloud_ServiceSingleSignOn($params901);
check('挂起实例的 SSO 被面板策略拒绝', empty($ssoSusp['success'])
    && strpos((string) ($ssoSusp['errorMsg'] ?? ''), '挂起') !== false,
    json_encode($ssoSusp, JSON_UNESCAPED_UNICODE));

// 解除挂起，恢复可控制台状态
try { $api->power($kvmId, 'unsuspend'); } catch (Throwable $e) {}
usleep(900000);

$ssoK = eyvescloud_ServiceSingleSignOn($params901);
check('KVM SSO 返回 vnc 控制台 或 明确未运行（而非被挂起误拦）',
    (!empty($ssoK['success']) && strpos((string) ($ssoK['redirectTo'] ?? ''), 'vnc') !== false)
    || strpos((string) ($ssoK['errorMsg'] ?? ''), '未运行') !== false,
    json_encode($ssoK, JSON_UNESCAPED_UNICODE));

/* ---------- 电源 ---------- */
echo "\n-- 电源动作 --\n";
$p = eyvescloud_PowerOn($params900);
check('开机返回 success 或结构化错误', $p === 'success' || strpos($p, '开机失败') === 0, $p);
foreach (['eyvescloud_PowerOff', 'eyvescloud_HardStop', 'eyvescloud_Reboot', 'eyvescloud_HardRestart'] as $fn) {
    $r = $fn($params900);
    check("$fn 不崩溃", is_string($r), $r);
}

/* ---------- 挂起/恢复 ---------- */
echo "\n-- 挂起与恢复 --\n";
$s = eyvescloud_SuspendAccount($params901);
check('挂起返回 success 或结构化错误', $s === 'success' || strpos($s, '挂起失败') === 0, $s);
$u = eyvescloud_UnsuspendAccount($params901);
check('恢复返回 success 或结构化错误', $u === 'success' || strpos($u, '恢复失败') === 0, $u);

/* ---------- 升降配 ---------- */
echo "\n-- 升降配 --\n";
$cp = eyvescloud_ChangePackage($params901);
check('升降配成功（内存 2048→2048 无变化也 success）', $cp === 'success' || strpos($cp, '升降配失败') === 0, $cp);
// 磁盘缩小必须被拒
$shrink = $params901;
$shrink['configoptions']['disk_gb'] = '5';   // 当前 40GB
$r = eyvescloud_ChangePackage($shrink);
check('磁盘缩小被拒', strpos($r, '不支持缩小') !== false, $r);

/* ---------- 改密 ---------- */
echo "\n-- 重置密码 --\n";
$cpw = eyvescloud_ChangePassword(array_merge($params900, ['password' => 'NewPw#2026']));
check('改密返回 success 或结构化错误', $cpw === 'success' || strpos($cpw, '改密失败') === 0, $cpw);

/* ---------- 未绑定服务不应崩 ---------- */
echo "\n-- 边界 --\n";
$unbound = ['serviceid' => 902, 'pid' => 31, 'serverid' => 7, 'configoptions' => []];
check('未绑定服务的 ClientArea 走错误模板', (eyvescloud_ClientArea($unbound)['templatefile'] ?? '') === 'templates/error');
check('未绑定服务的 Terminate 视为成功', eyvescloud_TerminateAccount($unbound) === 'success');
check('未绑定服务的电源返回结构化错误', strpos(eyvescloud_PowerOn($unbound), '开机失败') === 0);
$ssoUnbound = eyvescloud_ServiceSingleSignOn($unbound);
check('未绑定服务的 SSO 返回失败而非崩溃', empty($ssoUnbound['success']));

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
