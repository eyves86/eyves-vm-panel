<?php
/**
 * EyvesCloud WHMCS 服务器模块
 *
 * 支持 LXC 与 KVM 两种运行时并存：
 *   - 每个产品通过配置项 `runtime` 绑定一种运行时（LXC 产品 / KVM 产品）
 *   - 镜像下拉按 runtime 过滤，杜绝把 LXC 模板装到 KVM 产品上
 *   - 客户区能力（VNC / ISO / 救援）由 EyvesMapper 的 caps 位驱动
 *
 * 与既有 EyvesCloud WHMCS 模块的数据约定保持一致：
 *   实例 ID 存于产品自定义字段 `hostid`，老客户迁移无需改数据。
 */

if (!defined('WHMCS')) {
    die('This file cannot be accessed directly');
}

use WHMCS\Database\Capsule;
use WHMCS\Module\Server\EyvesCloud\EyvesCloud;
use WHMCS\Module\Server\EyvesCloud\EyvesCloudException;
use WHMCS\Module\Server\EyvesCloud\EyvesMapper;
use WHMCS\Module\Server\EyvesCloud\EyvesLang;

require_once __DIR__ . '/lib/EyvesCloud.php';
require_once __DIR__ . '/lib/EyvesMapper.php';
require_once __DIR__ . '/lib/EyvesLang.php';

const EYVESCLOUD_HOSTID_FIELD = 'hostid';
const EYVESCLOUD_RUNTIME_FIELD = 'runtime';
const EYVESCLOUD_VERSION = '1.0';

/* =====================================================================
 * 内部工具
 * =================================================================== */

if (!function_exists('eyvescloud_api')) {
    /** @return EyvesCloud */
    function eyvescloud_api(array $params)
    {
        return new EyvesCloud($params);
    }
}

if (!function_exists('eyvescloud_first_server_id')) {
    /** 产品未绑定服务器时，退化到本模块的第一台服务器（用于配置项拉镜像） */
    function eyvescloud_first_server_id()
    {
        try {
            $row = Capsule::table('tblservers')->where('type', 'eyvescloud')->orderBy('id')->first();
            if ($row && !empty($row->id)) {
                return (int) $row->id;
            }
            $row = Capsule::table('tblservers')->orderBy('id')->first();
            return $row && !empty($row->id) ? (int) $row->id : 0;
        } catch (Throwable $e) {
            return 0;
        }
    }
}

if (!function_exists('eyvescloud_get_hostid')) {
    /** 读取服务对应的面板实例 ID */
    function eyvescloud_get_hostid($serviceId, $productId = 0)
    {
        try {
            $q = Capsule::table('tblcustomfields')->where('fieldname', EYVESCLOUD_HOSTID_FIELD);
            if ($productId) {
                $q->where('relid', $productId);
            }
            $field = $q->first();
            if (!$field || empty($field->id)) {
                return '';
            }
            $val = Capsule::table('tblcustomfieldsvalues')
                ->where('fieldid', $field->id)
                ->where('relid', $serviceId)
                ->first();
            return $val && isset($val->value) ? trim((string) $val->value) : '';
        } catch (Throwable $e) {
            return '';
        }
    }
}

if (!function_exists('eyvescloud_set_hostid')) {
    function eyvescloud_set_hostid($serviceId, $value, $productId = 0)
    {
        try {
            $q = Capsule::table('tblcustomfields')->where('fieldname', EYVESCLOUD_HOSTID_FIELD);
            if ($productId) {
                $q->where('relid', $productId);
            }
            $field = $q->first();
            if (!$field || empty($field->id)) {
                return false;
            }
            $exists = Capsule::table('tblcustomfieldsvalues')
                ->where('fieldid', $field->id)->where('relid', $serviceId)->first();
            if ($exists) {
                Capsule::table('tblcustomfieldsvalues')
                    ->where('fieldid', $field->id)->where('relid', $serviceId)
                    ->update(['value' => $value]);
            } else {
                Capsule::table('tblcustomfieldsvalues')->insert([
                    'fieldid' => $field->id, 'relid' => $serviceId, 'value' => $value,
                ]);
            }
            return true;
        } catch (Throwable $e) {
            return false;
        }
    }
}

/**
 * 产品配置项 → 创建规格。
 * runtime 决定镜像族、控制台类型与可用能力，是 LXC/KVM 并存的枢纽。
 */
if (!function_exists('eyvescloud_spec_from_params')) {
    function eyvescloud_spec_from_params(array $params, $nameOverride = '')
    {
        $cfg = isset($params['configoptions']) && is_array($params['configoptions']) ? $params['configoptions'] : [];

        $runtime = 'lxc';
        foreach (['runtime', 'virtualization', 'virt_type'] as $k) {
            if (!empty($cfg[$k])) { $runtime = strtolower(trim((string) $cfg[$k])); break; }
        }
        // 兼容 "LXC (轻量)" / "KVM (完整虚拟化)" 这类带说明的选项值
        $runtime = (strpos($runtime, 'kvm') !== false) ? 'kvm' : 'lxc';

        $pick = function ($keys, $cfg, $default = null) {
            foreach ((array) $keys as $k) {
                if (isset($cfg[$k]) && $cfg[$k] !== '') { return $cfg[$k]; }
            }
            return $default;
        };

        $spec = [
            'runtime'     => $runtime,
            'name'        => $nameOverride !== '' ? $nameOverride : ('vm-' . ($params['serviceid'] ?? uniqid())),
            'template_id' => (string) $pick(['image', 'template', 'template_id', 'os'], $cfg, ''),
            'vcpu'        => (float) $pick(['vcpu', 'cpu', 'cores'], $cfg, 1),
            'memory_mb'   => (int) $pick(['memory_mb', 'memory', 'ram_mb', 'ram'], $cfg, 512),
            'disk_gb'     => (float) $pick(['disk_gb', 'disk'], $cfg, 10),
        ];

        $intOpts = [
            'data_disk_gb'      => 'float',
            'down_mbps'         => 'int',
            'up_mbps'           => 'int',
            'traffic_quota_gb'  => 'int',
            'ssh_port'          => 'int',
            'nat_ports'         => 'int',
            'public_ipv4_count' => 'int',
            'ipv6_count'        => 'int',
        ];
        foreach ($intOpts as $k => $t) {
            $v = $pick([$k], $cfg, null);
            if ($v !== null && $v !== '') {
                $spec[$k] = $t === 'float' ? (float) $v : (int) $v;
            }
        }
        $strOpts = ['node_id', 'storage_pool_id', 'tenant', 'traffic_mode', 'cloud_init'];
        foreach ($strOpts as $k) {
            $v = $pick([$k], $cfg, '');
            if ($v !== '') { $spec[$k] = (string) $v; }
        }
        if (!empty($cfg['firewall'])) {
            $spec['firewall_enabled'] = in_array(strtolower((string) $cfg['firewall']), ['on', '1', 'yes', 'true', '启用'], true);
        }

        // 密码：WHMCS 服务密码优先，否则由面板自动生成
        if (!empty($params['password'])) {
            $spec['auth'] = ['mode' => 'password', 'password' => (string) $params['password']];
        }

        return $spec;
    }
}

/* =====================================================================
 * 模块元数据与配置项
 * =================================================================== */

function eyvescloud_MetaData()
{
    return [
        'DisplayName'                 => 'EyvesCloud',
        'APIVersion'                  => '2.2',
        'RequiresServer'              => true,
        'DefaultNonSSLPort'           => '8999',
        'DefaultSSLPort'              => '443',
        'ServiceSingleSignOnLabel'    => '登录控制台',
        'AdminSingleSignOnLabel'      => '登录面板管理',
    ];
}

function eyvescloud_ConfigOptions()
{
    // 连接面板拉取真实镜像清单；失败则退化为纯文本输入，绝不阻断产品保存
    $lxcImages = [];
    $kvmImages = [];
    $connError = '';
    try {
        $sid = eyvescloud_first_server_id();
        if ($sid > 0) {
            $api = new EyvesCloud(['serverid' => $sid]);
            $grouped = $api->imagesGrouped();
            foreach ($grouped['lxc'] as $img) {
                if (empty($img['enabled'])) { continue; }
                $lxcImages[$img['id']] = $img['name'] . ' (' . ($img['arch'] ?? '') . ')';
            }
            foreach ($grouped['kvm'] as $img) {
                if (empty($img['enabled'])) { continue; }
                $kvmImages[$img['id']] = $img['name'] . ' (' . ($img['arch'] ?? '') . ')';
            }
        } else {
            $connError = '尚未添加 EyvesCloud 服务器';
        }
    } catch (Throwable $e) {
        $connError = '镜像清单拉取失败：' . $e->getMessage();
    }

    $imageOptions = [];
    if (!empty($lxcImages)) {
        $imageOptions['—— LXC 镜像 ——'] = '';
        foreach ($lxcImages as $k => $v) { $imageOptions[$v . '  [' . $k . ']'] = $k; }
    }
    if (!empty($kvmImages)) {
        $imageOptions['—— KVM 镜像 ——'] = '';
        foreach ($kvmImages as $k => $v) { $imageOptions[$v . '  [' . $k . ']'] = $k; }
    }

    return [
        'runtime' => [
            'FriendlyName' => '虚拟化类型',
            'Type'         => 'dropdown',
            'Options'      => ['lxc' => 'LXC（容器，轻量）', 'kvm' => 'KVM（完整虚拟化）'],
            'Default'      => 'lxc',
            'Description'  => '决定可用镜像族与控制台类型：LXC 用 SSH 终端，KVM 支持 VNC/ISO/救援',
        ],
        'image' => [
            'FriendlyName' => '系统镜像',
            'Type'         => count($imageOptions) > 0 ? 'dropdown' : 'text',
            'Options'      => $imageOptions,
            'Default'      => '',
            'Description'  => count($imageOptions) > 0
                ? '镜像已按运行时归类，请与上方「虚拟化类型」保持一致'
                : ('请填写镜像 ID。' . $connError),
        ],
        'vcpu' => [
            'FriendlyName' => 'CPU 核数',
            'Type' => 'text', 'Size' => '10', 'Default' => '1',
            'Description' => '支持小数（如 0.5）',
        ],
        'memory_mb' => [
            'FriendlyName' => '内存 (MB)',
            'Type' => 'text', 'Size' => '10', 'Default' => '512',
        ],
        'disk_gb' => [
            'FriendlyName' => '系统盘 (GB)',
            'Type' => 'text', 'Size' => '10', 'Default' => '10',
        ],
        'data_disk_gb' => [
            'FriendlyName' => '数据盘 (GB)',
            'Type' => 'text', 'Size' => '10', 'Default' => '0',
            'Description' => '填 0 表示不创建数据盘（LXC/KVM 均支持，单块）',
        ],
        'traffic_quota_gb' => [
            'FriendlyName' => '月流量 (GB)',
            'Type' => 'text', 'Size' => '10', 'Default' => '0',
            'Description' => '0 表示不限',
        ],
        'down_mbps' => ['FriendlyName' => '下行带宽 (Mbps)', 'Type' => 'text', 'Size' => '10', 'Default' => '100'],
        'up_mbps'   => ['FriendlyName' => '上行带宽 (Mbps)', 'Type' => 'text', 'Size' => '10', 'Default' => '50'],
        'nat_ports' => [
            'FriendlyName' => 'NAT 端口数',
            'Type' => 'text', 'Size' => '10', 'Default' => '0',
            'Description' => '分配给实例的端口映射数量上限',
        ],
        'public_ipv4_count' => ['FriendlyName' => '独立 IPv4 数', 'Type' => 'text', 'Size' => '10', 'Default' => '0'],
        'ipv6_count'        => ['FriendlyName' => 'IPv6 数量', 'Type' => 'text', 'Size' => '10', 'Default' => '0'],
        'firewall' => [
            'FriendlyName' => '防火墙',
            'Type' => 'dropdown', 'Options' => 'on,off', 'Default' => 'off',
        ],
        'node_id' => [
            'FriendlyName' => '指定节点',
            'Type' => 'text', 'Size' => '20', 'Default' => '',
            'Description' => '留空=本机主控；填 auto=调度器自动选择',
        ],
    ];
}

/* =====================================================================
 * 生命周期
 * =================================================================== */

function eyvescloud_TestConnection(array $params)
{
    try {
        $res = eyvescloud_api($params)->testConnection();
        if (empty($res['success'])) {
            return ['success' => false, 'error' => $res['error'] ?? '连接失败'];
        }
        return ['success' => true, 'error' => '', 'version' => $res['version'] ?? ''];
    } catch (Throwable $e) {
        return ['success' => false, 'error' => $e->getMessage()];
    }
}

function eyvescloud_CreateAccount(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $serviceId = (int) ($params['serviceid'] ?? 0);
        $productId = (int) ($params['pid'] ?? 0);

        // 幂等：已有绑定实例直接返回
        $existing = eyvescloud_get_hostid($serviceId, $productId);
        if ($existing !== '') {
            return 'success';
        }

        $name = !empty($params['domain']) ? $params['domain'] : ('vm-' . $serviceId);
        $name = preg_replace('/[^a-zA-Z0-9\-]/', '-', $name);
        $name = trim($name, '-');
        if ($name === '') { $name = 'vm-' . $serviceId; }

        $spec = eyvescloud_spec_from_params($params, $name);
        if ($spec['template_id'] === '') {
            return '创建失败：未配置系统镜像（产品配置项「系统镜像」）';
        }

        // 防呆：镜像必须属于产品的运行时。
        // 注意「该运行时一个镜像都没有」也要拦 —— 否则会一路走到面板，
        // 报出一个和配置无关的底层错误（如 "Template is not enabled or downloaded"）。
        try {
            $imgList = $api->images($spec['runtime']);
            $ids = array_column($imgList, 'id');
            if (empty($ids)) {
                return sprintf(
                    '创建失败：%s 运行时当前没有任何可用镜像。请确认宿主节点已具备该虚拟化能力（KVM 需要 /dev/kvm 与 virsh）。',
                    strtoupper($spec['runtime'])
                );
            }
            if (!in_array($spec['template_id'], $ids, true)) {
                return sprintf(
                    '创建失败：镜像 %s 不属于 %s 运行时（该运行时可用：%s）',
                    $spec['template_id'], strtoupper($spec['runtime']), implode(', ', array_slice($ids, 0, 5))
                );
            }
        } catch (Throwable $e) {
            // 拉不到镜像清单时不阻断创建，交给面板校验
        }

        $res = $api->createInstance($spec);
        $items = isset($res['items']) ? $res['items'] : [];
        $instanceRef = '';
        if (!empty($items) && isset($items[0]['name'])) {
            $instanceRef = (string) $items[0]['name'];
        }
        // 记下创建时返回的初始密码（面板只在创建响应里回一次）
        $initialPassword = isset($res['password']) ? (string) $res['password'] : '';

        $taskIds = isset($res['task_ids']) && is_array($res['task_ids']) ? $res['task_ids'] : [];

        // 绑定：优先用实例名，等任务完成后再换真实 ID
        if ($instanceRef !== '') {
            eyvescloud_set_hostid($serviceId, $instanceRef, $productId);
        }

        if (!empty($taskIds)) {
            $last = $api->waitTask($taskIds[0], 180, 3);
            $st = strtolower((string) ($last['status'] ?? ''));
            if (in_array($st, ['failed', 'error'], true)) {
                eyvescloud_set_hostid($serviceId, '', $productId);
                return '创建失败：' . ($last['message'] ?? $last['error'] ?? '面板任务失败');
            }
        }

        // 任务结束后把 hostid 换成面板真实 ID（数字 ID 或 UUID）
        try {
            $found = $api->findInstanceByName($instanceRef);
            if ($found && !empty($found['id'])) {
                eyvescloud_set_hostid($serviceId, (string) $found['id'], $productId);
            }
        } catch (Throwable $e) {
            // 名字绑定依然可用，忽略
        }

        if ($initialPassword !== '') {
            try {
                Capsule::table('tblhosting')->where('id', $serviceId)->update([
                    'password' => encrypt($initialPassword),
                ]);
            } catch (Throwable $e) {
                // 非致命
            }
        }

        return 'success';
    } catch (EyvesCloudException $e) {
        return '创建失败：' . $e->getMessage();
    } catch (Throwable $e) {
        return '创建失败：' . $e->getMessage();
    }
}

function eyvescloud_SuspendAccount(array $params)
{
    return eyvescloud_power_call($params, 'suspend', '挂起失败');
}

function eyvescloud_UnsuspendAccount(array $params)
{
    return eyvescloud_power_call($params, 'unsuspend', '恢复失败');
}

function eyvescloud_TerminateAccount(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $serviceId = (int) ($params['serviceid'] ?? 0);
        $productId = (int) ($params['pid'] ?? 0);
        $hostid = eyvescloud_get_hostid($serviceId, $productId);
        if ($hostid === '') {
            return 'success'; // 无绑定视为已销毁
        }
        $ref = eyvescloud_resolve_id($api, $hostid);
        // 销毁走回收站软删除，与 WHMCS 终止语义对齐（可恢复窗口）
        $api->deleteInstance($ref, false);
        eyvescloud_set_hostid($serviceId, '', $productId);
        return 'success';
    } catch (EyvesCloudException $e) {
        return '销毁失败：' . $e->getMessage();
    } catch (Throwable $e) {
        return '销毁失败：' . $e->getMessage();
    }
}

function eyvescloud_ChangePassword(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $pw = isset($params['password']) ? (string) $params['password'] : '';
        $api->resetPassword($ref, $pw);
        return 'success';
    } catch (EyvesCloudException $e) {
        return '改密失败：' . $e->getMessage();
    } catch (Throwable $e) {
        return '改密失败：' . $e->getMessage();
    }
}

/**
 * 升降配。LXC/KVM 均可在线调整 vCPU/内存/带宽；
 * 磁盘只增不减（面板语义），缩小请求直接拒绝并说明。
 */
function eyvescloud_ChangePackage(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $cur = eyvescloud_capability_instance($api, $ref);
        $spec = eyvescloud_spec_from_params($params, $cur['name'] ?? '');

        $patch = [];
        if ($spec['vcpu'] > 0)      { $patch['vcpu'] = $spec['vcpu']; }
        if ($spec['memory_mb'] > 0) { $patch['memory_mb'] = $spec['memory_mb']; }
        if ($spec['disk_gb'] > 0) {
            if ($spec['disk_gb'] < (float) ($cur['disk_gb'] ?? 0)) {
                return sprintf('升降配失败：磁盘不支持缩小（当前 %.0f GB，请求 %.0f GB）', $cur['disk_gb'], $spec['disk_gb']);
            }
            $patch['disk_gb'] = $spec['disk_gb'];
        }
        foreach (['data_disk_gb', 'down_mbps', 'up_mbps', 'traffic_quota_gb'] as $k) {
            if (isset($spec[$k]) && $spec[$k] > 0) { $patch[$k] = $spec[$k]; }
        }
        if (empty($patch)) {
            return 'success';
        }
        $api->updateInstance($ref, $patch);
        return 'success';
    } catch (EyvesCloudException $e) {
        return '升降配失败：' . $e->getMessage();
    } catch (Throwable $e) {
        return '升降配失败：' . $e->getMessage();
    }
}

function eyvescloud_AdminCustomButtonArray()
{
    return [
        '开机'         => 'PowerOn',
        '关机'         => 'PowerOff',
        '强制关机'     => 'HardStop',
        '重启'         => 'Reboot',
        '强制重启'     => 'HardRestart',
        '重装系统'     => 'Reinstall',
        '重置密码'     => 'ResetPassword',
        '重置流量'     => 'ResetTraffic',
    ];
}

function eyvescloud_PowerOn(array $params)      { return eyvescloud_power_call($params, 'start', '开机失败'); }
function eyvescloud_PowerOff(array $params)     { return eyvescloud_power_call($params, 'shutdown', '关机失败'); }
function eyvescloud_HardStop(array $params)     { return eyvescloud_power_call($params, 'hard-stop', '强制关机失败'); }
function eyvescloud_Reboot(array $params)       { return eyvescloud_power_call($params, 'restart', '重启失败'); }
function eyvescloud_HardRestart(array $params)  { return eyvescloud_power_call($params, 'hard-restart', '强制重启失败'); }

function eyvescloud_Reinstall(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $spec = eyvescloud_spec_from_params($params);
        if ($spec['template_id'] === '') {
            return '重装失败：产品未配置系统镜像';
        }
        $api->reinstall($ref, $spec['template_id'], 'password', (string) ($params['password'] ?? ''));
        return 'success';
    } catch (Throwable $e) {
        return '重装失败：' . $e->getMessage();
    }
}

function eyvescloud_ResetPassword(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $res = $api->resetPassword($ref, (string) ($params['password'] ?? ''));
        // 面板未指定密码时会回传新密码，落回 WHMCS
        $newPw = is_array($res) && !empty($res['password']) ? (string) $res['password'] : '';
        if ($newPw !== '') {
            Capsule::table('tblhosting')->where('id', (int) $params['serviceid'])->update(['password' => encrypt($newPw)]);
            return 'success|新密码：' . $newPw;
        }
        return 'success';
    } catch (Throwable $e) {
        return '重置密码失败：' . $e->getMessage();
    }
}

function eyvescloud_ResetTraffic(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $api->call('POST', '/api/v1/containers/' . rawurlencode($ref) . '/traffic-reset');
        return 'success';
    } catch (Throwable $e) {
        return '重置流量失败：' . $e->getMessage();
    }
}

/* =====================================================================
 * 客户区
 * =================================================================== */

function eyvescloud_ClientArea(array $params)
{
    try {
        EyvesLang::load();
        $lang = eyvescloud_lang_array();

        $api = eyvescloud_api($params);
        $serviceId = (int) ($params['serviceid'] ?? 0);
        $productId = (int) ($params['pid'] ?? 0);
        $hostid = eyvescloud_get_hostid($serviceId, $productId);
        if ($hostid === '') {
            return ['templatefile' => 'templates/error', 'vars' => [
                'message' => $lang['not_provisioned'] ?? '该服务尚未开通实例',
                'lang'    => $lang,
                'serviceid' => $serviceId,
                'assets'    => eyvescloud_assets_url(),
                'evVersion' => eyvescloud_asset_version(),
                'webRoot'   => eyvescloud_web_root(),
            ]];
        }

        $ref  = eyvescloud_resolve_id($api, $hostid);
        $raw  = $api->instance($ref);
        $vm   = EyvesMapper::instance($raw);

        // ---- 补齐展示用派生字段（缺失一律安全降级，绝不让模板拿到 undefined）----
        // 状态 → 语言键。不要拼字符串（曾经拼出 status_on 这个不存在的键，
        // 导致运行中的实例显示原始英文串 "running"）。
        $statusKeyMap = [
            'running'  => 'on',
            'stopped'  => 'off',
            'starting' => 'starting',
            'stopping' => 'stopping',
            'creating' => 'creating',
            'error'    => 'status_error',
            'unknown'  => 'status_unknown',
        ];
        $vKey = isset($statusKeyMap[$vm['status']]) ? $statusKeyMap[$vm['status']] : 'status_unknown';
        $vm['status_label'] = isset($lang[$vKey]) && $lang[$vKey] !== '' ? $lang[$vKey] : $vm['status'];
        $vm['os_family'] = eyvescloud_os_family($vm['template_id']);
        $region = eyvescloud_resolve_region($api, $vm['node_id']);
        $vm['region_code'] = $region['code'];
        $vm['region_name'] = $region['name'];

        // ---- 当前页签 ----
        $page = isset($_REQUEST['page']) ? preg_replace('/[^a-z]/', '', (string) $_REQUEST['page']) : 'base';
        if ($page === '') { $page = 'base'; }
        $caps = $vm['caps'];
        // 服务端同样按能力位把关：手动构造 URL 也进不去不该看的页签
        $allowed = ['base', 'monitor', 'network', 'drive', 'sshkey', 'password', 'crons', 'tasks', 'setting'];
        if (!empty($caps['port_map']))  { $allowed[] = 'portmap'; }
        if (!empty($caps['snapshot']))  { $allowed[] = 'snapshots'; }
        if (!empty($caps['backup']))    { $allowed[] = 'backups'; }
        if (!empty($caps['firewall']))  { $allowed[] = 'securitys'; }
        if (!empty($caps['iso_mount'])) { $allowed[] = 'iso'; }
        if (!in_array($page, $allowed, true)) {
            $page = 'base';
        }

        // ---- 页签数据 ----
        $data = eyvescloud_page_data($api, $vm, $page);

        // ---- 监控图表数据（服务端转换，模板只负责画）----
        $charts     = [];
        $chartsJson = '[]';
        $ranges     = [];
        if ($page === 'monitor') {
            $rangeKeys = [
                ['key' => '1h',  'label' => $lang['range_1h']  ?? '1h'],
                ['key' => '6h',  'label' => $lang['range_6h']  ?? '6h'],
                ['key' => '24h', 'label' => $lang['range_24h'] ?? '24h'],
                ['key' => '7d',  'label' => $lang['range_7d']  ?? '7d'],
            ];
            $curRange = isset($_REQUEST['range']) ? (string) $_REQUEST['range'] : '1h';
            foreach ($rangeKeys as $rk) {
                $rk['active'] = ($rk['key'] === $curRange);
                $ranges[] = $rk;
            }
            $charts = eyvescloud_build_charts(isset($data['metrics']) ? $data['metrics'] : null, $curRange, $lang);
            $chartsJson = json_encode($charts, JSON_UNESCAPED_UNICODE);
            $data['hasData'] = !empty($charts);
        }

        // ---- 只把前端 JS 真正用到的词条下发给浏览器 ----
        // （整份字典 172 条全塞进去纯属浪费，而且会在 DOM 里制造假信号）
        // 这份清单来自模板里 EvClient.lang.* / LANG.* 的实际引用，改模板时记得同步。
        $jsKeys = [
            'panel_unreachable', 'operation_failed', 'operation_success', 'copied',
            'actionconfirmtitle', 'warn_irreversible', 'cancel', 'confirm', 'close',
            'submit', 'create', 'save', 'delete', 'restore', 'refresh', 'loading',
            'boot', 'shutdown', 'hardshutdown', 'reboot', 'hardreboot', 'console',
            'port_add', 'internal_port', 'external_port', 'protocol', 'remark',
            'random_port', 'confirm_delete',
            'snapshot_name', 'snapshot_create', 'snapshot_restore',
            'backup_create',
            'reinstallos', 'reinstall_confirm', 'reinstall_tip', 'reinstall_name_mismatch',
            'select_image', 'image_not_downloaded',
            'password', 'new_password', 'password_rule', 'password_reset_ok',
            'reset_traffic_ok',
            'rescue_enter', 'rescue_pick_iso',
        ];
        $langJs = [];
        foreach ($jsKeys as $k) {
            $langJs[$k] = isset($lang[$k]) ? $lang[$k] : '';
        }

        return [
            'templatefile' => 'templates/clientarea',
            'vars' => [
                'vm'          => $vm,
                'caps'        => $caps,
                'console'     => $vm['console'],
                'page'        => $page,
                'data'        => $data,
                'charts'      => $charts,
                'chartsJson'  => $chartsJson,
                'ranges'      => $ranges,
                'lang'        => $lang,
                'langJson'    => json_encode($langJs, JSON_UNESCAPED_UNICODE),
                'serviceid'   => $serviceId,
                'webRoot'     => eyvescloud_web_root(),
                'assets'      => eyvescloud_assets_url(),
                'api'         => eyvescloud_api_url(),
                'evVersion'   => eyvescloud_asset_version(),
                'pageAlert'   => null,
            ],
        ];
    } catch (Throwable $e) {
        EyvesLang::load();
        return ['templatefile' => 'templates/error', 'vars' => [
            'message'   => $e->getMessage(),
            'lang'      => eyvescloud_lang_array(),
            'serviceid' => (int) ($params['serviceid'] ?? 0),
            'assets'    => eyvescloud_assets_url(),
            'evVersion' => eyvescloud_asset_version(),
        ]];
    }
}

function eyvescloud_AdminServicesTabFields(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $serviceId = (int) ($params['serviceid'] ?? 0);
        $productId = (int) ($params['pid'] ?? 0);
        $hostid = eyvescloud_get_hostid($serviceId, $productId);
        if ($hostid === '') {
            return ['面板实例 ID' => '<input type="text" name="eyves_hostid" value="" size="40" placeholder="留空并在下方绑定">'];
        }
        $ref = eyvescloud_resolve_id($api, $hostid);
        $model = EyvesMapper::instance($api->instance($ref));
        return [
            '面板实例 ID' => htmlspecialchars($model['id']),
            '运行时'      => $model['runtime_label'],
            '状态'        => $model['status'] . ($model['suspended'] ? '（已挂起）' : ''),
            '主 IP'       => htmlspecialchars($model['primary_ip']),
            '节点'        => htmlspecialchars($model['node_name']),
            '规格'        => sprintf('%.2f 核 / %d MB / %.0f GB + %.0f GB', $model['vcpu'], $model['memory_mb'], $model['disk_gb'], $model['data_disk_gb']),
            '流量'        => sprintf('%.2f / %.0f GB', $model['traffic']['used_gb'], $model['traffic']['quota_gb']),
            '到期'        => htmlspecialchars($model['expires_at']),
        ];
    } catch (Throwable $e) {
        return ['状态' => '读取失败：' . htmlspecialchars($e->getMessage())];
    }
}

function eyvescloud_ServiceSingleSignOn(array $params)
{
    try {
        $api = eyvescloud_api($params);
        $ref = eyvescloud_require_instance($api, $params);
        $model = EyvesMapper::instance($api->instance($ref));
        // LXC → SSH 终端；KVM → VNC。由 caps 决定，不在这里判断 runtime
        $console = $api->console($model);
        $url = $console['url'] ?? '';
        return ['success' => true, 'redirectTo' => $url];
    } catch (Throwable $e) {
        return ['success' => false, 'errorMsg' => $e->getMessage()];
    }
}

/* =====================================================================
 * 共享内部实现
 * =================================================================== */

if (!function_exists('eyvescloud_power_call')) {
    function eyvescloud_power_call(array $params, $action, $errPrefix)
    {
        try {
            $api = eyvescloud_api($params);
            $ref = eyvescloud_require_instance($api, $params);
            $api->power($ref, $action);
            return 'success';
        } catch (EyvesCloudException $e) {
            return $errPrefix . '：' . $e->getMessage();
        } catch (Throwable $e) {
            return $errPrefix . '：' . $e->getMessage();
        }
    }
}

if (!function_exists('eyvescloud_resolve_id')) {
    /**
     * hostid 可能是面板数字 ID、UUID 或实例名（创建瞬间绑定的是名字）。
     * 统一解析成可用于 URL 的标识。
     */
    function eyvescloud_resolve_id(EyvesCloud $api, $hostid)
    {
        $hostid = trim((string) $hostid);
        if ($hostid === '') {
            throw new EyvesCloudException('未绑定面板实例');
        }
        // 直接按 ID 探测
        try {
            $api->instance($hostid);
            return $hostid;
        } catch (Throwable $e) {
            // 退化为按名字查找
        }
        $found = $api->findInstanceByName($hostid);
        if ($found && !empty($found['id'])) {
            return (string) $found['id'];
        }
        throw new EyvesCloudException('面板中找不到实例：' . $hostid);
    }
}

if (!function_exists('eyvescloud_require_instance')) {
    function eyvescloud_require_instance(EyvesCloud $api, array $params)
    {
        $hostid = eyvescloud_get_hostid((int) ($params['serviceid'] ?? 0), (int) ($params['pid'] ?? 0));
        return eyvescloud_resolve_id($api, $hostid);
    }
}

if (!function_exists('eyvescloud_capability_instance')) {
    function eyvescloud_capability_instance(EyvesCloud $api, $ref)
    {
        return $api->instance($ref);
    }
}

/* =====================================================================
 * 客户区辅助（模板上下文构造）
 * =================================================================== */

if (!function_exists('eyvescloud_lang_array')) {
    function eyvescloud_lang_array()
    {
        global $_LANG;
        return is_array($_LANG) ? $_LANG : [];
    }
}

if (!function_exists('eyvescloud_web_root')) {
    function eyvescloud_web_root()
    {
        try {
            if (class_exists('\WHMCS\Config\Setting')) {
                $root = \WHMCS\Config\Setting::getValue('SystemURL');
                if (!empty($root)) {
                    return rtrim($root, '/');
                }
            }
        } catch (Throwable $e) {
            // 降级为相对路径
        }
        return '';
    }
}

if (!function_exists('eyvescloud_module_url')) {
    function eyvescloud_module_url()
    {
        return eyvescloud_web_root() . '/modules/servers/eyvescloud';
    }
}

if (!function_exists('eyvescloud_asset_version')) {
    /**
     * 静态资源版本号 = 关键文件的 mtime。
     * 写死常量会让浏览器永远吃缓存（改样式不生效，升级后客户看到的还是旧界面），
     * 用 mtime 才能改完即生效、升级即失效。
     */
    function eyvescloud_asset_version()
    {
        static $ver = null;
        if ($ver !== null) {
            return $ver;
        }
        $files = [
            __DIR__ . '/templates/assets/css/eyves.css',
            __DIR__ . '/templates/javascript.tpl',
        ];
        $max = 0;
        foreach ($files as $f) {
            if (is_file($f)) {
                $t = @filemtime($f);
                if ($t && $t > $max) { $max = $t; }
            }
        }
        $ver = $max > 0 ? (string) $max : EYVESCLOUD_VERSION;
        return $ver;
    }
}

if (!function_exists('eyvescloud_assets_url')) {
    function eyvescloud_assets_url()
    {
        return eyvescloud_module_url() . '/templates/assets/';
    }
}

if (!function_exists('eyvescloud_api_url')) {
    function eyvescloud_api_url()
    {
        return eyvescloud_module_url() . '/api_client.php';
    }
}

if (!function_exists('eyvescloud_os_family')) {
    /**
     * 从镜像 ID 推导操作系统图标族。
     * 只用于给 font-os 图标挑字形，识别不出就回落到通用图标，不参与任何业务判断。
     */
    function eyvescloud_os_family($templateId)
    {
        $id = strtolower((string) $templateId);
        $map = [
            // 只映射 font-os 图标集真实存在的字形（见 assets/css/font-os/os/），
            // 不在集合里的系统（alpine/kali/gentoo/oracle…）返回空串，
            // 由模板回落到通用图标 —— 总比渲染出一个空白方块好。
            'ubuntu'   => 'ubuntu',
            'debian'   => 'debian',
            'centos'   => 'centos',
            'alma'     => 'almalinux',
            'rocky'    => 'rockylinux',
            'fedora'   => 'fedora',
            'arch'     => 'archlinux',
            'opensuse' => 'opensuse',
            'suse'     => 'opensuse',
            'freebsd'  => 'freebsd',
            'openbsd'  => 'openbsd',
            'windows'  => 'windows',
            'win'      => 'windows',
            'coreos'   => 'coreos',
            'rancher'  => 'rancheros',
        ];
        foreach ($map as $needle => $family) {
            if ($needle !== '' && strpos($id, $needle) !== false) {
                return $family;
            }
        }
        return '';
    }
}

if (!function_exists('eyvescloud_resolve_region')) {
    /**
     * 实例 → 区域展示信息（供区域徽标用）。
     * 面板的实例视图不带 region 字段，需经 node → region 反查；任何一步失败都安全降级。
     */
    function eyvescloud_resolve_region(EyvesCloud $api, $nodeId)
    {
        $out = ['code' => '', 'name' => ''];
        if ($nodeId === '') {
            return $out;
        }
        try {
            $node = null;
            foreach ($api->nodes() as $n) {
                if (isset($n['id']) && (string) $n['id'] === (string) $nodeId) {
                    $node = $n;
                    break;
                }
            }
            if (!$node) {
                return $out;
            }
            $out['name'] = isset($node['name']) ? (string) $node['name'] : '';
            $regionId = isset($node['region_id']) ? (string) $node['region_id'] : '';
            if ($regionId === '') {
                return $out;
            }
            foreach ($api->regions() as $r) {
                if (isset($r['id']) && (string) $r['id'] === $regionId) {
                    $out['name'] = isset($r['name']) ? (string) $r['name'] : $out['name'];
                    foreach (['code', 'short_name', 'slug'] as $k) {
                        if (!empty($r[$k])) {
                            $out['code'] = strtolower(substr((string) $r[$k], 0, 2));
                            break;
                        }
                    }
                    break;
                }
            }
        } catch (Throwable $e) {
            // 区域信息仅为装饰，取不到不影响页面
        }
        return $out;
    }
}

if (!function_exists('eyvescloud_page_data')) {
    /**
     * 按页签抓取服务端数据。所有请求失败都降级为空数组，页面照样渲染。
     */
    function eyvescloud_page_data(EyvesCloud $api, array $vm, $page)
    {
        $id = $vm['id'];
        switch ($page) {
            case 'monitor':
                $range = isset($_REQUEST['range']) ? (string) $_REQUEST['range'] : '1h';
                return [
                    'metrics' => $api->metrics($id, $range),
                    'usage'   => $api->usage($id),
                ];

            case 'portmap':
                $ports = $api->portMappings($id);
                $norm  = [];
                foreach ((array) $ports as $i => $p) {
                    if (!is_array($p)) { continue; }
                    $norm[] = [
                        'index'         => isset($p['index']) ? $p['index'] : $i,
                        'external_port' => isset($p['external_port']) ? $p['external_port'] : (isset($p['host_port']) ? $p['host_port'] : ''),
                        'internal_port' => isset($p['internal_port']) ? $p['internal_port'] : (isset($p['container_port']) ? $p['container_port'] : ''),
                        'protocol'      => isset($p['protocol']) ? $p['protocol'] : 'tcp',
                        'remark'        => isset($p['remark']) ? $p['remark'] : '',
                    ];
                }
                return ['ports' => $norm, 'limit' => (int) $vm['port_limit'], 'used' => count($norm)];

            case 'snapshots':
                $list = EyvesMapper::snapshotList($api->snapshots($id));
                return ['snapshots' => $list, 'count' => count($list)];

            case 'backups':
                return ['backups' => EyvesMapper::backupList($api->backups($id))];

            case 'securitys':
                $groups = $api->securityGroups();
                $attachedRaw = $api->instanceSecurityGroups($id);
                $attached = [];
                if (is_array($attachedRaw)) {
                    $src = isset($attachedRaw['items']) ? $attachedRaw['items'] : $attachedRaw;
                    foreach ((array) $src as $g) {
                        if (is_array($g) && isset($g['id'])) { $attached[] = (string) $g['id']; }
                        elseif (is_string($g)) { $attached[] = $g; }
                    }
                }
                $norm = [];
                foreach ((array) $groups as $g) {
                    if (!is_array($g)) { continue; }
                    $norm[] = [
                        'id'          => isset($g['id']) ? (string) $g['id'] : '',
                        'name'        => isset($g['name']) ? (string) $g['name'] : '',
                        'description' => isset($g['description']) ? (string) $g['description'] : '',
                        'rule_count'  => isset($g['rule_count']) ? (int) $g['rule_count'] : null,
                    ];
                }
                return ['groups' => $norm, 'attached' => $attached, 'ruleLabel' => 'Rules'];

            case 'sshkey':
                return ['keys' => $api->sshKeys()];

            case 'iso':
                return ['isos' => $api->isoImages()];

            case 'crons':
                $schedule = isset($vm['snapshot_schedule']) && is_array($vm['snapshot_schedule'])
                    ? $vm['snapshot_schedule'] : [];
                return ['schedule' => [
                    'enabled'        => !empty($schedule['enabled']),
                    'interval_hours' => isset($schedule['interval_hours']) ? (int) $schedule['interval_hours'] : 0,
                    'time'           => isset($schedule['time']) ? (string) $schedule['time'] : '',
                    'last_run'       => isset($schedule['last_run']) ? (string) $schedule['last_run'] : '',
                    'next_run'       => isset($schedule['next_run']) ? (string) $schedule['next_run'] : '',
                ]];

            case 'tasks':
                $all = EyvesMapper::taskList($api->tasks());
                $mine = [];
                foreach ($all as $t) {
                    if ($t['instance'] === '' || $t['instance'] === $vm['name']) {
                        $mine[] = $t;
                    }
                }
                return ['tasks' => $mine];

            case 'setting':
                // 关键：重装镜像必须按本实例的运行时过滤，杜绝 LXC 镜像装到 KVM
                return ['images' => EyvesMapper::imageOptions($api->images($vm['runtime']), $vm['runtime'])];

            default:
                return [];
        }
    }
}

if (!function_exists('eyvescloud_build_charts')) {
    /**
     * 把面板 metrics 响应转成 flot 可用的数据点。
     * 面板返回结构未知/为空时返回空数组，模板会走「无数据」空态。
     */
    function eyvescloud_build_charts($metrics, $range, array $lang)
    {
        if (!is_array($metrics)) {
            return [];
        }
        // 兼容两种形态：{series:[{key,points}]} 或 {cpu:[{t,v}], memory:[...]}
        $series = [];
        if (isset($metrics['series']) && is_array($metrics['series'])) {
            $series = $metrics['series'];
        } else {
            foreach ($metrics as $k => $v) {
                if (is_array($v) && isset($v[0])) {
                    $series[] = ['key' => $k, 'points' => $v];
                }
            }
        }
        if (empty($series)) {
            return [];
        }

        $meta = [
            'cpu'    => ['label' => $lang['monitor_cpu'] ?? 'CPU',    'unit' => '%',  'color' => '#2563eb'],
            'ram'    => ['label' => $lang['monitor_memory'] ?? 'RAM', 'unit' => '%',  'color' => '#12a150'],
            'memory' => ['label' => $lang['monitor_memory'] ?? 'RAM', 'unit' => '%',  'color' => '#12a150'],
            'disk'   => ['label' => $lang['monitor_disk'] ?? 'Disk',  'unit' => '%',  'color' => '#d97706'],
            'net'    => ['label' => $lang['monitor_net'] ?? 'Net',    'unit' => 'KB/s', 'color' => '#7c3aed'],
        ];

        $out = [];
        foreach ($series as $s) {
            if (!is_array($s)) { continue; }
            $key = isset($s['key']) ? (string) $s['key'] : '';
            $points = isset($s['points']) && is_array($s['points']) ? $s['points'] : [];
            $pts = [];
            foreach ($points as $p) {
                if (is_array($p)) {
                    $t = isset($p['t']) ? $p['t'] : (isset($p['timestamp']) ? $p['timestamp'] : null);
                    $v = isset($p['v']) ? $p['v'] : (isset($p['value']) ? $p['value'] : null);
                } elseif (is_object($p)) {
                    $t = isset($p->t) ? $p->t : (isset($p->timestamp) ? $p->timestamp : null);
                    $v = isset($p->v) ? $p->v : (isset($p->value) ? $p->value : null);
                } else {
                    continue;
                }
                if ($t === null || $v === null) { continue; }
                if (!is_numeric($t)) { $t = strtotime((string) $t); }
                // flot 的 time 模式要毫秒
                $pts[] = [(float) $t * 1000, (float) $v];
            }
            if (empty($pts)) { continue; }
            $m = isset($meta[$key]) ? $meta[$key] : ['label' => strtoupper($key), 'unit' => '', 'color' => '#2563eb'];
            $out[] = [
                'key'     => $key,
                'label'   => $m['label'],
                'unit'    => $m['unit'],
                'color'   => $m['color'],
                'current' => round((float) end($pts)[1], 2),
                'points'  => $pts,
            ];
        }
        return $out;
    }
}

