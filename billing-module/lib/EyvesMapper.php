<?php
/**
 * EyvesCloud → WHMCS 模块展示模型映射层
 *
 * 原则：
 *   1. 模板/前端**只认 caps 能力位**，不认 runtime 字符串。
 *      这样 LXC 与 KVM 共用一套 UI，差异只有能力开关。
 *   2. 所有字段名在这里一次性归一，禁止在模板里再判断。
 *   3. 缺失字段一律给安全默认值，模板永不因 undefined 崩溃。
 */

namespace WHMCS\Module\Server\EyvesCloud;

class EyvesMapper
{
    /** 能力矩阵：runtime → 能力位（唯一真相来源） */
    const CAPABILITY_MATRIX = [
        'lxc' => [
            'console_vnc'  => false,
            'console_ssh'  => true,
            'iso_mount'    => false,
            'rescue'       => false,
            'data_disk'    => true,
            'snapshot'     => true,
            'backup'       => true,
            'firewall'     => true,
            'port_map'     => true,
            'reinstall'    => true,
            'reset_pw'     => true,
            'cloud_init'   => true,
            'hard_power'   => true,   // lxc-stop -k / 强杀
        ],
        'kvm' => [
            'console_vnc'  => true,
            'console_ssh'  => true,
            'iso_mount'    => true,
            'rescue'       => true,
            'data_disk'    => true,
            'snapshot'     => true,
            'backup'       => true,
            'firewall'     => true,
            'port_map'     => true,
            'reinstall'    => true,
            'reset_pw'     => true,
            'cloud_init'   => true,
            'hard_power'   => true,   // virsh destroy/reset
        ],
    ];

    /** 状态归一（面板内部状态 → 前台统一状态） */
    const STATUS_MAP = [
        'running'  => 'running',
        'stopped'  => 'stopped',
        'stopping' => 'stopping',
        'starting' => 'starting',
        'creating' => 'creating',
        'error'    => 'error',
        'unknown'  => 'unknown',
    ];

    /**
     * 取运行时的**唯一**判定点。
     * v1 的字段叫 virtualization，v2 叫 runtime —— 只认 v2 的话会把所有实例
     * 都判成 LXC，KVM 的 VNC / ISO / 救援能力会整片消失。
     */
    public static function runtimeOf(array $instance)
    {
        $rt = '';
        foreach (['virtualization', 'runtime', 'type'] as $k) {
            if (!empty($instance[$k])) { $rt = strtolower(trim((string) $instance[$k])); break; }
        }
        return $rt === 'kvm' ? 'kvm' : 'lxc';
    }

    public static function capabilities($runtime)
    {
        $runtime = $runtime === 'kvm' ? 'kvm' : 'lxc';
        return self::CAPABILITY_MATRIX[$runtime];
    }

    public static function statusOf(array $instance)
    {
        $st = strtolower(trim((string) (isset($instance['status']) ? $instance['status'] : 'unknown')));
        return isset(self::STATUS_MAP[$st]) ? self::STATUS_MAP[$st] : 'unknown';
    }

    /** 状态是否可开机 / 可关机 */
    public static function canPowerOn(array $instance)
    {
        $st = self::statusOf($instance);
        if (self::isSuspended($instance)) {
            return false;
        }
        return in_array($st, ['stopped', 'error'], true);
    }

    public static function canPowerOff(array $instance)
    {
        return in_array(self::statusOf($instance), ['running', 'starting'], true);
    }

    public static function isSuspended(array $instance)
    {
        return !empty($instance['suspended']);
    }

    public static function isLocked(array $instance)
    {
        return !empty($instance['locked']);
    }

    /**
     * 时间归一：面板返回 RFC3339（"2026-09-01T00:00:00Z"），
     * 直接展示既长又难读，统一成 "Y-m-d H:i"。解析失败原样返回，绝不吞掉信息。
     */
    public static function time($value)
    {
        $value = trim((string) $value);
        if ($value === '') {
            return '';
        }
        // 已经是友好格式就不再动
        if (preg_match('/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}/', $value)) {
            return substr($value, 0, 16);
        }
        $ts = strtotime($value);
        if ($ts === false || $ts <= 0) {
            return $value;
        }
        return gmdate('Y-m-d H:i', $ts);
    }

    /**
     * 核心：把面板实例视图映射为模块统一模型。
     */
    /**
     * v1 的面板对象是**扁平的原始结构**（字段名与 v2 的展示视图完全不同），
     * 归一化集中在这里做，模板与其它调用方只见统一模型。
     *
     * 与 v2 的关键差异：
     *   ram_mb → memory_mb       ip → primary_ip        vnc_port 是扁平的
     *   network_down_mbps → bandwidth.down_mbps
     *   traffic_used_rx 是**字节**（v2 已经除过 1024³）
     *   port_mappings 内联在容器对象里（v2 是 nat_ports）
     *   suspended / locked / data_disk_gb 等 omitempty，缺失即 false/0
     */
    public static function instance(array $c, array $extra = [])
    {
        $runtime = self::runtimeOf($c);
        $caps    = self::capabilities($runtime);
        $status  = self::statusOf($c);

        // omitempty 字段一律给安全默认
        $ports   = isset($c['port_mappings']) && is_array($c['port_mappings']) ? $c['port_mappings'] : [];
        $pubV4   = isset($c['public_ipv4s']) && is_array($c['public_ipv4s']) ? $c['public_ipv4s'] : [];
        $pubV6   = isset($c['ipv6_addresses']) && is_array($c['ipv6_addresses']) ? $c['ipv6_addresses'] : [];

        // 流量：v1 给的是字节，统一换成 GB（保留两位）
        $toGb = function ($bytes) {
            return round(((float) $bytes) / 1073741824, 2);
        };
        $usedRx  = $toGb(isset($c['traffic_used_rx']) ? $c['traffic_used_rx'] : 0);
        $usedTx  = $toGb(isset($c['traffic_used_tx']) ? $c['traffic_used_tx'] : 0);
        $quotaGb = isset($c['monthly_traffic_gb']) ? (float) $c['monthly_traffic_gb'] : 0.0;
        $used    = $usedRx + $usedTx;

        $consoleType = $caps['console_vnc'] ? 'vnc' : 'ssh';
        $sched = [
            'enabled'        => !empty($c['snapshot_schedule_enabled']),
            'interval_hours' => isset($c['snapshot_schedule_interval_hours']) ? (int) $c['snapshot_schedule_interval_hours'] : 0,
            'time'           => isset($c['snapshot_schedule_time']) ? (string) $c['snapshot_schedule_time'] : '',
            'last_run'       => isset($c['snapshot_schedule_last_run']) ? (string) $c['snapshot_schedule_last_run'] : '',
            'next_run'       => isset($c['snapshot_schedule_next_run']) ? (string) $c['snapshot_schedule_next_run'] : '',
        ];

        $model = [
            // 标识
            'id'              => isset($c['id']) ? $c['id'] : '',
            'uuid'            => isset($c['uuid']) ? $c['uuid'] : '',
            'name'            => isset($c['name']) ? (string) $c['name'] : '',
            'hostname'        => isset($c['name']) ? (string) $c['name'] : '',

            // 运行时与状态
            'runtime'         => $runtime,
            'runtime_label'   => $runtime === 'kvm' ? 'KVM' : 'LXC',
            'status'          => $status,
            'status_raw'      => isset($c['status']) ? (string) $c['status'] : 'unknown',
            'suspended'       => !empty($c['suspended']),
            'suspend_reason'  => isset($c['suspended_reason']) ? (string) $c['suspended_reason'] : '',
            'locked'          => !empty($c['locked']),

            // 规格
            'vcpu'            => isset($c['vcpu']) ? (float) $c['vcpu'] : 0.0,
            'memory_mb'       => isset($c['ram_mb']) ? (int) $c['ram_mb'] : 0,
            'disk_gb'         => isset($c['disk_gb']) ? (float) $c['disk_gb'] : 0.0,
            'data_disk_gb'    => isset($c['data_disk_gb']) ? (float) $c['data_disk_gb'] : 0.0,

            // 网络
            'primary_ip'      => isset($c['ip']) ? (string) $c['ip'] : '',
            'public_ipv4'     => $pubV4,
            'ipv6_addresses'  => $pubV6,
            'ipv6'            => isset($c['ipv6']) ? (string) $c['ipv6'] : '',
            'ssh_port'        => isset($c['ssh_port']) ? (int) $c['ssh_port'] : 0,
            'mac_address'     => isset($c['mac_address']) ? (string) $c['mac_address'] : '',
            'vnc_port'        => isset($c['vnc_port']) ? (int) $c['vnc_port'] : 0,
            'domain_name'     => isset($c['kvm_name']) ? (string) $c['kvm_name'] : '',
            'bandwidth'       => [
                'down_mbps' => isset($c['network_down_mbps']) ? (int) $c['network_down_mbps'] : 0,
                'up_mbps'   => isset($c['network_up_mbps']) ? (int) $c['network_up_mbps'] : 0,
            ],
            'nat_ports'       => count($ports),
            'port_mappings'   => $ports,
            'port_limit'      => isset($c['port_mapping_limit']) ? (int) $c['port_mapping_limit'] : 0,

            // 流量
            'traffic'         => [
                'quota_gb'   => $quotaGb,
                'used_gb'    => round($used, 2),
                'used_rx_gb' => $usedRx,
                'used_tx_gb' => $usedTx,
                'percent'    => $quotaGb > 0 ? round($used / $quotaGb * 100, 2) : 0.0,
                'mode'       => isset($c['traffic_mode']) ? (string) $c['traffic_mode'] : 'total',
            ],

            // 配额
            'snapshot_limit'  => isset($c['snapshot_limit']) ? (int) $c['snapshot_limit'] : 0,
            'io_limits'       => [
                'read_mbps'  => isset($c['io_read_mbps']) ? (int) $c['io_read_mbps'] : 0,
                'write_mbps' => isset($c['io_write_mbps']) ? (int) $c['io_write_mbps'] : 0,
            ],
            'firewall_enabled'   => !empty($c['firewall_enabled']),
            'firewall_default_action' => isset($c['firewall_default_action']) ? (string) $c['firewall_default_action'] : '',
            'rescue_enabled'     => !empty($c['rescue_enabled']),
            'policy_blocked'     => !empty($c['policy_blocked']),
            'snapshot_schedule'  => $sched,

            // 归属
            'node_id'         => isset($c['node_id']) ? (string) $c['node_id'] : '',
            'node_name'       => isset($c['node_id']) && $c['node_id'] !== '' ? (string) $c['node_id'] : '本机（主控）',
            'node_status'     => isset($c['node_id']) && $c['node_id'] !== '' ? 'remote' : 'local',
            'template_id'     => isset($c['template']) ? (string) $c['template'] : '',
            'storage_pool_id' => isset($c['storage_pool_id']) ? (string) $c['storage_pool_id'] : '',
            'owner'           => isset($c['owner']) ? (string) $c['owner'] : 'admin',
            'tenant'          => isset($c['tenant']) ? (string) $c['tenant'] : '',
            'remark'          => isset($c['remark']) ? (string) $c['remark'] : '',

            // 时间
            'created_at'      => self::time(isset($c['created_at']) ? $c['created_at'] : ''),
            'created_at_raw'  => isset($c['created_at']) ? (string) $c['created_at'] : '',
            'expires_at'      => self::time(isset($c['expires_at']) ? $c['expires_at'] : ''),
            'expires_at_raw'  => isset($c['expires_at']) ? (string) $c['expires_at'] : '',

            // 能力位（模板只读这里）
            'caps'            => $caps,
            'console'         => [
                'type'  => $consoleType,
                'label' => $consoleType === 'vnc' ? 'VNC 控制台' : 'SSH 终端',
                'proto' => $consoleType === 'vnc' ? 'noVNC' : 'WebSSH',
            ],
        ];

        foreach ($extra as $k => $v) {
            $model[$k] = $v;
        }
        return $model;
    }

    public static function instanceList(array $items)
    {
        $out = [];
        foreach ($items as $it) {
            if (is_array($it)) {
                $out[] = self::instance($it);
            }
        }
        return $out;
    }

    /**
     * 镜像 → 前台下拉项（按 runtime 过滤由调用方保证）
     */
    public static function image(array $img)
    {
        return [
            'id'          => isset($img['id']) ? (string) $img['id'] : '',
            'name'        => isset($img['name']) ? (string) $img['name'] : '',
            'runtime'     => isset($img['type']) ? (string) $img['type'] : (isset($img['runtime']) ? (string) $img['runtime'] : 'lxc'),
            'distro'      => isset($img['distro']) ? (string) $img['distro'] : '',
            'release'     => isset($img['release']) ? (string) $img['release'] : '',
            'arch'        => isset($img['arch']) ? (string) $img['arch'] : '',
            'description' => isset($img['description']) ? (string) $img['description'] : '',
            'downloaded'  => !empty($img['downloaded']),
            'enabled'     => !isset($img['enabled']) || !empty($img['enabled']),
            'size_mb'     => isset($img['size_bytes']) ? round(((float) $img['size_bytes']) / 1048576, 1) : 0,
            'label'       => trim((isset($img['name']) ? $img['name'] : '') . ' (' . (isset($img['arch']) ? $img['arch'] : '') . ')'),
        ];
    }

    public static function imageList(array $items)
    {
        $out = [];
        foreach ($items as $it) {
            if (is_array($it)) {
                $out[] = self::image($it);
            }
        }
        return $out;
    }

    /** 镜像按 runtime 分组（产品配置与重装下拉用） */
    public static function imageOptions(array $images, $runtime = null)
    {
        $out = [];
        foreach (self::imageList($images) as $img) {
            if ($runtime !== null && $img['runtime'] !== $runtime) {
                continue;
            }
            if (!$img['enabled']) {
                continue;
            }
            $out[] = ['id' => $img['id'], 'name' => $img['label'], 'runtime' => $img['runtime'], 'downloaded' => $img['downloaded']];
        }
        return $out;
    }

    public static function task(array $t)
    {
        return [
            'id'         => isset($t['id']) ? (string) $t['id'] : '',
            'action'     => isset($t['action']) ? (string) $t['action'] : '',
            'status'     => isset($t['status']) ? (string) $t['status'] : 'unknown',
            'progress'   => isset($t['progress']) ? (int) $t['progress'] : 0,
            'message'    => isset($t['message']) ? (string) $t['message'] : '',
            'instance'   => isset($t['container']) ? (string) $t['container'] : (isset($t['container_name']) ? (string) $t['container_name'] : ''),
            'created_at' => isset($t['created_at']) ? (string) $t['created_at'] : '',
            'finished_at' => isset($t['finished_at']) ? (string) $t['finished_at'] : '',
            'error'      => isset($t['error']) ? (string) $t['error'] : '',
        ];
    }

    public static function taskList(array $items)
    {
        $out = [];
        foreach ($items as $it) {
            if (is_array($it)) {
                $out[] = self::task($it);
            }
        }
        return $out;
    }

    public static function snapshot(array $s)
    {
        return [
            'id'         => isset($s['id']) ? (string) $s['id'] : '',
            'name'       => isset($s['name']) ? (string) $s['name'] : '',
            'description' => isset($s['description']) ? (string) $s['description'] : '',
            'size_mb'    => isset($s['size_bytes']) ? round(((float) $s['size_bytes']) / 1048576, 1) : (isset($s['size_mb']) ? (float) $s['size_mb'] : 0),
            'created_at' => isset($s['created_at']) ? (string) $s['created_at'] : '',
            'status'     => isset($s['status']) ? (string) $s['status'] : 'ready',
        ];
    }

    public static function snapshotList(array $items)
    {
        $out = [];
        foreach ($items as $it) {
            if (is_array($it)) {
                $out[] = self::snapshot($it);
            }
        }
        return $out;
    }

    public static function backup(array $b)
    {
        return [
            'id'         => isset($b['id']) ? (string) $b['id'] : '',
            'name'       => isset($b['name']) ? (string) $b['name'] : '',
            'size_mb'    => isset($b['size_bytes']) ? round(((float) $b['size_bytes']) / 1048576, 1) : (isset($b['size_mb']) ? (float) $b['size_mb'] : 0),
            'created_at' => isset($b['created_at']) ? (string) $b['created_at'] : '',
            'status'     => isset($b['status']) ? (string) $b['status'] : 'ready',
        ];
    }

    public static function backupList(array $items)
    {
        $out = [];
        foreach ($items as $it) {
            if (is_array($it)) {
                $out[] = self::backup($it);
            }
        }
        return $out;
    }

    /**
     * 面板错误 → WHMCS 模块统一错误结构（模板/钩子可直接展示）
     */
    public static function error($message, $code = '')
    {
        return ['success' => false, 'status' => 'error', 'error' => (string) $message, 'code' => (string) $code];
    }

    public static function ok($data = null, $message = '')
    {
        return ['success' => true, 'status' => 'success', 'message' => (string) $message, 'data' => $data];
    }
}
