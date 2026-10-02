<?php
/**
 * EyvesCloud 适配层冒烟测试（脱离 WHMCS 运行）
 * 用法：php tests/smoke.php [baseHost] [port]
 */
require_once __DIR__ . '/../lib/EyvesCloud.php';

use WHMCS\Module\Server\EyvesCloud\EyvesCloud;
use WHMCS\Module\Server\EyvesCloud\EyvesCloudException;

$host = isset($argv[1]) ? $argv[1] : '127.0.0.1';
$port = isset($argv[2]) ? (int) $argv[2] : 8998;

$serverRow = [
    'id'         => 1,
    'hostname'   => $host,
    'ipaddress'  => $host,
    'port'       => $port,
    'secure'     => 0,
    'username'   => 'admin',
    'password'   => 'Adapter#2026',
    'accesshash' => json_encode(['timeout' => 20]),
];

$pass = 0; $fail = 0;
function check($name, $cond, $extra = '') {
    global $pass, $fail;
    if ($cond) { $pass++; echo "  ✅ $name\n"; }
    else { $fail++; echo "  ❌ $name" . ($extra !== '' ? "  ($extra)" : '') . "\n"; }
}

echo "=== EyvesCloud 适配层冒烟测试 @ $host:$port ===\n\n";

try {
    $api = new EyvesCloud([], $serverRow);
} catch (Throwable $e) {
    echo "构造失败: " . $e->getMessage() . "\n"; exit(1);
}

// 1. 登录
try { $tok = $api->login(); check('登录获取 token', strlen($tok) > 20, 'len=' . strlen($tok)); }
catch (Throwable $e) { check('登录获取 token', false, $e->getMessage()); exit(1); }

// 2. 连接自检
$t = $api->testConnection();
check('testConnection', !empty($t['success']), json_encode($t, JSON_UNESCAPED_UNICODE));

// 3. 镜像按 runtime 分组
$imgs = $api->imagesGrouped();
$lxcN = count($imgs['lxc']); $kvmN = count($imgs['kvm']);
check("镜像分组 (lxc=$lxcN, kvm=$kvmN)", $lxcN > 0, json_encode(array_keys($imgs)));
$dupId = array_intersect(
    array_column($imgs['lxc'], 'id'),
    array_column($imgs['kvm'], 'id')
);
check('LXC/KVM 镜像 ID 无交叉污染', count($dupId) === 0, json_encode($dupId));
check('镜像带运行时字段（v1 叫 type）', ($imgs['lxc'][0]['runtime'] ?? '') === 'lxc', json_encode($imgs['lxc'][0] ?? []));

// 4. 过滤接口
$lxcOnly = $api->images(EyvesCloud::RUNTIME_LXC);
check('按 lxc 过滤后全为 lxc', count(array_filter($lxcOnly, fn($i) => EyvesCloud::RUNTIME_LXC !== self_runtime($i))) === 0);
$kvmOnly = $api->images(EyvesCloud::RUNTIME_KVM);
check('按 kvm 过滤后全为 kvm', count(array_filter($kvmOnly, fn($i) => EyvesCloud::RUNTIME_KVM !== self_runtime($i))) === 0);
echo "     （KVM 镜像数 " . count($kvmOnly) . " —— v1 把 KVM 镜像门控在宿主机具备 /dev/kvm+virsh 之后，为 0 属正常）\n";
function self_runtime($i) { return strtolower((string)($i['type'] ?? $i['runtime'] ?? '')); }

// 5. 目录类
check('regions', is_array($api->regions()));
check('nodes', is_array($api->nodes()));
check('ipPools', is_array($api->ipPools()));
$sp = $api->storagePools();
check('storagePools 返回数组', is_array($sp), json_encode($sp));
check('sshKeys', is_array($api->sshKeys()));
check('securityGroups', is_array($api->securityGroups()));
check('isoImages', is_array($api->isoImages()));
check('instances', is_array($api->instances()));
check('metricsSummary', is_array($api->metricsSummary()));

// 6. 运行时语义矩阵
$fakeLxc = ['id' => 'x', 'runtime' => 'lxc'];
$fakeKvm = ['id' => 'x', 'runtime' => 'kvm'];
check('LXC 控制台类型 = ssh', $api->consoleType($fakeLxc) === 'ssh');
check('KVM 控制台类型 = vnc', $api->consoleType($fakeKvm) === 'vnc');
check('LXC 不支持 VNC', $api->supportsVNC($fakeLxc) === false);
check('KVM 支持 VNC', $api->supportsVNC($fakeKvm) === true);
check('LXC 不支持 ISO/救援', $api->supportsISO($fakeLxc) === false && $api->supportsRescue($fakeLxc) === false);
check('KVM 支持 ISO/救援', $api->supportsISO($fakeKvm) === true && $api->supportsRescue($fakeKvm) === true);
check('runtime 归一化(垃圾值→lxc)', $api->normalizeRuntime('plasma') === 'lxc');

// 7. LXC 请求 VNC 必须本地拦截
try {
    $api->console($fakeLxc, 'vnc');
    check('LXC 请求 VNC 被拦截', false, '未抛异常');
} catch (EyvesCloudException $e) {
    check('LXC 请求 VNC 被拦截', true);
}

// 8. 电源动作映射
$mapOk = true;
foreach (['start','stop','shutdown','restart','hard-stop','hard-restart','suspend','unsuspend'] as $a) {
    // 不实际执行，只验证映射表存在（非法值应抛错）
    try { $api->power('__nonexistent__', 'bogus-action'); $mapOk = false; } catch (EyvesCloudException $e) {}
    break;
}
check('非法电源动作被拒绝', $mapOk);

// 9. 产品配置 → 创建规格
$spec = $api->specFromProduct([
    'runtime' => 'kvm', 'name' => 'kvm-test', 'template_id' => 'ubuntu-noble',
    'vcpu' => 2, 'memory_mb' => 2048, 'disk_gb' => 40, 'data_disk_gb' => 100,
    'down_mbps' => 100, 'up_mbps' => 50, 'ssh_port' => 0,
]);
check('specFromProduct runtime=kvm', $spec['runtime'] === 'kvm');
check('specFromProduct 数值类型正确', is_float($spec['vcpu']) && is_int($spec['memory_mb']));
check('specFromProduct 数据盘', isset($spec['data_disk_gb']) && $spec['data_disk_gb'] == 100);

// 10. 错误信封
$env = $api->raw('GET', '/api/v2/instances/__no_such_id__');
check('404 返回错误信封且不抛异常', $env['ok'] === false && $env['status'] === 404, json_encode($env['code']));
check('错误信封含 message', $env['message'] !== '');

// 11. 错误凭据
$bad = new EyvesCloud([], array_merge($serverRow, ['password' => 'wrong-password']));
try { $bad->login(); check('错误密码抛异常', false); }
catch (EyvesCloudException $e) { check('错误密码抛异常', true, $e->getMessage()); }

// 12. 不可达面板
$dead = new EyvesCloud([], array_merge($serverRow, ['port' => 59999]));
try { $dead->login(); check('不可达面板抛异常', false); }
catch (EyvesCloudException $e) { check('不可达面板抛异常', strpos($e->getMessage(), '无法连接') !== false, $e->getMessage()); }

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
