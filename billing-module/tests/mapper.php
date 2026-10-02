<?php
/**
 * 映射层验证：LXC 与 KVM 同模型、能力位正确、缺字段不崩
 * 用法：php tests/mapper.php [host] [port]   （带参则同时跑真机数据）
 */
require_once __DIR__ . '/../lib/EyvesCloud.php';
require_once __DIR__ . '/../lib/EyvesMapper.php';

use WHMCS\Module\Server\EyvesCloud\EyvesCloud;
use WHMCS\Module\Server\EyvesCloud\EyvesMapper as M;

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

echo "=== 映射层验证 ===\n\n";

/* ---------- 1. 纯单元：极端/残缺输入 ---------- */
echo "-- 残缺输入健壮性 --\n";
$empty = M::instance([]);
check('空数组不崩', is_array($empty) && isset($empty['caps']));
check('空数组默认 runtime=lxc', $empty['runtime'] === 'lxc');
check('空数组默认 status=unknown', $empty['status'] === 'unknown');
check('空数组 caps 齐全', count($empty['caps']) === 13);
check('空数组 console.type=ssh', $empty['console']['type'] === 'ssh');
check('traffic 除零保护', $empty['traffic']['percent'] === 0.0);

$weird = M::instance(['virtualization' => 'XEN', 'status' => 'FROZEN', 'vcpu' => '2.5', 'ram_mb' => '1024']);
check('未知 virtualization 落回 lxc', $weird['runtime'] === 'lxc');
check('未知 status 落回 unknown', $weird['status'] === 'unknown');
check('字符串数值被转换', $weird['vcpu'] === 2.5 && $weird['memory_mb'] === 1024);
check('缺 vnc_port 时为 0', $weird['vnc_port'] === 0);
check('缺流量字段时为 0', $weird['traffic']['used_gb'] === 0.0);
check('缺端口映射时为 0', $weird['nat_ports'] === 0);

/* ---------- 2. 能力矩阵互斥性 ---------- */
echo "\n-- 能力矩阵 --\n";
$lxcCaps = M::capabilities('lxc');
$kvmCaps = M::capabilities('kvm');
check('LXC 无 VNC', $lxcCaps['console_vnc'] === false);
check('KVM 有 VNC', $kvmCaps['console_vnc'] === true);
check('两者都有 SSH', $lxcCaps['console_ssh'] === true && $kvmCaps['console_ssh'] === true);
check('LXC 无 ISO/救援', $lxcCaps['iso_mount'] === false && $lxcCaps['rescue'] === false);
check('KVM 有 ISO/救援', $kvmCaps['iso_mount'] === true && $kvmCaps['rescue'] === true);
check('两者都支持数据盘', $lxcCaps['data_disk'] === true && $kvmCaps['data_disk'] === true);
check('两者都支持硬电源', $lxcCaps['hard_power'] === true && $kvmCaps['hard_power'] === true);

/* ---------- 3. 状态机 ---------- */
echo "\n-- 电源可用性 --\n";
check('running 可关机不可开机', M::canPowerOff(['status' => 'running']) && !M::canPowerOn(['status' => 'running']));
check('stopped 可开机不可关机', M::canPowerOn(['status' => 'stopped']) && !M::canPowerOff(['status' => 'stopped']));
check('挂起后禁止开机', !M::canPowerOn(['status' => 'stopped', 'suspended' => true]));
check('挂起实例 isSuspended=true', M::isSuspended(['suspended' => true]));

/* ---------- 3.5 端口映射计数（v2 返回的是数组，不能 (int) 强转） ---------- */
echo "\n-- 端口映射计数 --\n";
$mp = M::instance(['port_mappings' => [['external_port' => 8080], ['external_port' => 8081]], 'port_mapping_limit' => 5]);
check('port_mappings → 计数为 2', $mp['nat_ports'] === 2, (string) $mp['nat_ports']);
check('原始映射列表被保留', count($mp['port_mappings']) === 2);
check('port_limit 来自 port_mapping_limit', $mp['port_limit'] === 5);
check('空列表 → 0', M::instance(['port_mappings' => []])['nat_ports'] === 0);
check('字段缺失 → 0', M::instance([])['nat_ports'] === 0);
check('异常类型不炸（字符串）', M::instance(['port_mappings' => 'abc'])['nat_ports'] === 0);

// 流量：v1 给字节，必须换算成 GB
$tb = M::instance(['traffic_used_rx' => 1073741824, 'traffic_used_tx' => 536870912, 'monthly_traffic_gb' => 10, 'traffic_mode' => 'total']);
check('字节→GB 换算（rx 1G）', $tb['traffic']['used_rx_gb'] === 1.0, (string) $tb['traffic']['used_rx_gb']);
check('字节→GB 换算（tx 0.5G）', $tb['traffic']['used_tx_gb'] === 0.5);
check('已用为 rx+tx', $tb['traffic']['used_gb'] === 1.5);
check('百分比计算', $tb['traffic']['percent'] === 15.0, (string) $tb['traffic']['percent']);

/* ---------- 4. 镜像按 runtime 分组 ---------- */
echo "\n-- 镜像分组 --\n";
$fake = [
    ['id' => 'l1', 'name' => 'Debian 12', 'runtime' => 'lxc', 'arch' => 'amd64', 'enabled' => true, 'size_bytes' => 104857600],
    ['id' => 'k1', 'name' => 'Debian 12', 'runtime' => 'kvm', 'arch' => 'amd64', 'enabled' => true],
    ['id' => 'l2', 'name' => '已禁用', 'runtime' => 'lxc', 'enabled' => false],
];
check('runtime 过滤只留 lxc', count(M::imageOptions($fake, 'lxc')) === 1, json_encode(array_column(M::imageOptions($fake, 'lxc'), 'id')));
check('runtime 过滤只留 kvm', count(M::imageOptions($fake, 'kvm')) === 1);
check('未指定 runtime 返回全部启用项', count(M::imageOptions($fake)) === 2);
check('disabled 被剔除', !in_array('l2', array_column(M::imageOptions($fake, 'lxc'), 'id')));
check('size 换算 MB', M::image($fake[0])['size_mb'] == 100.0);

/* ---------- 5. 真机数据（可选） ---------- */
if (isset($argv[1])) {
    $host = $argv[1]; $port = isset($argv[2]) ? (int) $argv[2] : 8998;
    echo "\n-- 真机数据映射 --\n";
    $api = new EyvesCloud([], [
        'hostname' => $host, 'ipaddress' => $host, 'port' => $port, 'secure' => 0,
        'username' => 'admin', 'password' => 'Adapter#2026', 'accesshash' => '{}',
    ]);
    $raw = $api->instances();
    $mapped = M::instanceList($raw);
    check('真机实例映射数量一致', count($mapped) === count($raw), count($mapped) . ' vs ' . count($raw));
    $byName = [];
    foreach ($mapped as $m) { $byName[$m['name']] = $m; }
    if (isset($byName['ct-lxc-01'], $byName['ct-kvm-01'])) {
        $l = $byName['ct-lxc-01']; $k = $byName['ct-kvm-01'];
        check('真机 LXC caps.console_vnc=false', $l['caps']['console_vnc'] === false);
        check('真机 KVM caps.console_vnc=true', $k['caps']['console_vnc'] === true);
        check('真机 LXC console.type=ssh', $l['console']['type'] === 'ssh');
        check('真机 KVM console.type=vnc', $k['console']['type'] === 'vnc');
        check('真机 KVM vnc_port 带出', $k['vnc_port'] === 59102, (string) $k['vnc_port']);
        check('真机 LXC vnc_port=0', $l['vnc_port'] === 0);
        check('真机 data_disk_gb 带出', $k['data_disk_gb'] == 100.0);
        check('真机模块化后无 v2 遗留字段', !array_key_exists('kvm', $l) && array_key_exists('port_mappings', $l));
    } else {
        check('真机实例存在', false, '未找到种入实例');
    }
    $imgs = $api->images();
    check('真机镜像映射（LXC 至少有一项）', count(M::imageOptions($imgs, 'lxc')) > 0);
    // v1 把 KVM 镜像门控在 hostKVMAvailable() 之后（需要 /dev/kvm + virsh）。
    // 沙箱没有 KVM，因此返回 0 个是**正确行为**，不能当成失败。
    $kvmN = count(M::imageOptions($imgs, 'kvm'));
    echo "     （本机 KVM 镜像数：{$kvmN} —— 0 属正常，取决于宿主机是否具备 KVM）\n";
}

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
