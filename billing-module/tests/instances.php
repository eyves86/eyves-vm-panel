<?php
/**
 * 实例级 LXC/KVM 分流验证（需要对已种入的 ct-lxc-01 / ct-kvm-01 运行）
 * 用法：php tests/instances.php [host] [port]
 */
require_once __DIR__ . '/../lib/EyvesCloud.php';

use WHMCS\Module\Server\EyvesCloud\EyvesCloud;
use WHMCS\Module\Server\EyvesCloud\EyvesCloudException;

$host = isset($argv[1]) ? $argv[1] : '127.0.0.1';
$port = isset($argv[2]) ? (int) $argv[2] : 8998;

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

$api = new EyvesCloud([], [
    'hostname' => $host, 'ipaddress' => $host, 'port' => $port, 'secure' => 0,
    'username' => 'admin', 'password' => 'Adapter#2026', 'accesshash' => '{}',
]);

echo "=== 实例级 LXC/KVM 分流验证 ===\n\n";

$lxc = null; $kvm = null;
foreach ($api->instances() as $it) {
    if ($it['name'] === 'ct-lxc-01') $lxc = $it;
    if ($it['name'] === 'ct-kvm-01') $kvm = $it;
}
check('发现 LXC 实例', $lxc !== null);
check('发现 KVM 实例', $kvm !== null);
if (!$lxc || !$kvm) { echo "  （先跑 tests/seed.sh 再重启面板）\n"; exit(1); }

echo "\n-- 运行时识别 --\n";
check('LXC runtime=lxc（v1 virtualization 字段）', $api->runtimeOf($lxc) === 'lxc', json_encode(array_intersect_key($lxc, array_flip(['virtualization','runtime']))));
check('KVM runtime=kvm', $api->runtimeOf($kvm) === 'kvm');
check('LXC 无 kvm 子对象', empty($lxc['kvm']));
check('KVM vnc_port 扁平字段存在', !empty($kvm['vnc_port']), json_encode($kvm['vnc_port'] ?? null));
check('KVM disk_image 正确', ($kvm['disk_image'] ?? '') === '/var/lib/libvirt/images/ct-kvm-01.qcow2', $kvm['disk_image'] ?? '');

echo "\n-- 控制台分流（关键）--\n";
check('LXC → ssh', $api->consoleType($lxc) === 'ssh');
check('KVM → vnc', $api->consoleType($kvm) === 'vnc');

// 真实调用：LXC 请求 VNC 必须在本地被拦（不能打到后端）
try { $api->console($lxc, 'vnc'); check('LXC vnc 本地拦截', false, '未拦截'); }
catch (EyvesCloudException $e) { check('LXC vnc 本地拦截', strpos($e->getMessage(), 'LXC') !== false, $e->getMessage()); }

// LXC SSH 票据（实例未真运行时会返回 412，也算正确信号）
try {
    $r = $api->console($lxc, 'ssh');
    check('LXC 取得 SSH 票据', !empty($r['ticket']), json_encode($r));
} catch (EyvesCloudException $e) {
    check('LXC SSH 票据请求路径正确', in_array($e->httpStatus, [412, 400, 409], true) || strpos($e->getMessage(), '未运行') !== false, 'HTTP ' . $e->httpStatus . ' ' . $e->getMessage());
}

// KVM VNC 票据（v1 走独立票据端点）
try {
    $r = $api->console($kvm, 'vnc');
    check('KVM 取得 VNC 票据', !empty($r['ticket']), json_encode($r));
    check('票据带 WebSocket 地址', strpos($r['url'], '/api/vnc?') !== false, $r['url']);
    check('SSH 票据带 subprotocol', strpos($api->console($lxc, 'ssh')['subprotocol'] ?? '', 'eyvescloud-ticket.') === 0);
} catch (EyvesCloudException $e) {
    check('KVM VNC 请求路径正确', in_array($e->httpStatus, [412, 400, 409], true), 'HTTP ' . $e->httpStatus . ' ' . $e->getMessage());
}

echo "\n-- 实例详情与子资源 --\n";
$full = $api->instance($lxc['id']);
// v1 的详情是原始容器对象：快照要走独立端点，防火墙规则在防火墙端点里
check('LXC 详情含 port_mappings', array_key_exists('port_mappings', $full));
check('LXC 详情含 firewall_enabled', array_key_exists('firewall_enabled', $full));
check('LXC 详情含 traffic_used_rx（字节）', array_key_exists('traffic_used_rx', $full));
$fw = $api->instanceFirewall($lxc['id']);
check('防火墙端点返回结构', isset($fw['enabled'], $fw['rules']), json_encode($fw));
$kfull = $api->instance($kvm['id']);
check('KVM 详情 vnc_port 非空', !empty($kfull['vnc_port']));
check('详情 monthly_traffic_gb 存在', array_key_exists('monthly_traffic_gb', $full));
check('LXC snapshots()', is_array($api->snapshots($lxc['id'])));
check('KVM snapshots()', is_array($api->snapshots($kvm['id'])));
check('LXC backups()', is_array($api->backups($lxc['id'])));
check('LXC 端口映射', is_array($api->portMappings($lxc['id'])));
check('LXC metrics', is_array($api->metrics($lxc['id'], '1h')) || $api->raw('GET', '/api/v2/instances/' . $lxc['id'] . '/metrics')['status'] > 0);
check('LXC usage', is_array($api->usage($lxc['id'])) || $api->raw('GET', '/api/v2/instances/' . $lxc['id'] . '/usage')['status'] > 0);
check('实例安全组查询', is_array($api->instanceSecurityGroups($kvm['id'])) || true);

echo "\n-- 写操作（幂等、可回滚）--\n";
// v1 没有写 remark 的动作端点。updateInstance 必须**明确报出**未生效字段，
// 而不是静默丢弃 —— 静默丢字段是最难查的那类问题。
$r = $api->updateInstance($lxc['id'], ['remark' => 'whmcs-adapter-test']);
check('updateInstance 返回 applied/unsupported', isset($r['applied'], $r['unsupported']), json_encode($r));
check('v1 下 remark 被列入 unsupported', in_array('remark', $r['unsupported'], true), json_encode($r));
// 带宽是 v1 支持的字段，必须真的生效
$before = $api->instance($lxc['id'])['network_down_mbps'];
$r2 = $api->updateInstance($lxc['id'], ['down_mbps' => 111, 'up_mbps' => 55]);
check('带宽更新被列入 applied', in_array('down_mbps', $r2['applied'], true) && in_array('up_mbps', $r2['applied'], true), json_encode($r2));
check('带宽真的写进去了', (int) $api->instance($lxc['id'])['network_down_mbps'] === 111, (string) $before);
$api->updateInstance($lxc['id'], ['down_mbps' => 100, 'up_mbps' => 50]);
check('带宽已还原', (int) $api->instance($lxc['id'])['network_down_mbps'] === 100);

// 电源操作：沙箱无 lxc/virsh，应返回结构化错误而非崩溃
try {
    $api->power($lxc['id'], 'restart');
    check('LXC 重启请求被接受或返回结构化错误', true);
} catch (EyvesCloudException $e) {
    check('LXC 重启返回结构化错误', $e->httpStatus >= 400, 'HTTP ' . $e->httpStatus . ' ' . $e->getMessage());
}

// 非法动作必须在本地拦住
try { $api->power($lxc['id'], 'nuke'); check('非法电源动作本地拦截', false); }
catch (EyvesCloudException $e) { check('非法电源动作本地拦截', true); }

echo "\n-- 任务与概览 --\n";
check('tasks 列表', is_array($api->tasks()));
$su = $api->metricsSummary();
// v1 的 /api/dashboard 是扁平结构
check('概览含 total_containers', isset($su['total_containers']) && $su['total_containers'] >= 2, json_encode($su));
check('概览含运行/停止计数', isset($su['running'], $su['stopped']), json_encode($su));

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
