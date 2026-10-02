<?php
/**
 * EyvesCloud WHMCS 客户区 API 路由
 *
 * 前端（templates/*.tpl + assets）只与本文件通信，不直连面板。
 * 所有 LXC/KVM 差异都在这里终结：每个响应都带 caps 能力位，
 * 前端按 caps 决定显示 VNC 按钮还是 SSH 终端按钮、是否显示 ISO/救援页签。
 *
 * 约定：统一返回 {status:"success"|"error", message, data}
 */

define('CLIENTAREA', true);
require('../../../init.php');

use WHMCS\Database\Capsule;
use WHMCS\Module\Server\EyvesCloud\EyvesCloud;
use WHMCS\Module\Server\EyvesCloud\EyvesCloudException;
use WHMCS\Module\Server\EyvesCloud\EyvesMapper;

require_once __DIR__ . '/lib/EyvesCloud.php';
require_once __DIR__ . '/lib/EyvesMapper.php';

/* ---------------------------------------------------------------------
 * 响应工具
 * ------------------------------------------------------------------- */

function ev_json($payload)
{
    header('Content-Type: application/json; charset=utf-8');
    die(json_encode($payload, JSON_UNESCAPED_UNICODE));
}

function ev_ok($data = null, $message = '')
{
    ev_json(['status' => 'success', 'message' => $message, 'data' => $data]);
}

function ev_err($message, $code = '')
{
    ev_json(['status' => 'error', 'message' => $message, 'code' => $code]);
}

/* ---------------------------------------------------------------------
 * 鉴权与实例解析
 * ------------------------------------------------------------------- */

$userid  = isset($_SESSION['uid']) ? (int) $_SESSION['uid'] : 0;
$isAdmin = isset($_SESSION['adminid']) ? (int) $_SESSION['adminid'] : 0;
if (!$userid && !$isAdmin) {
    ev_err('未登录，请先登录', 'NOT_LOGGED_IN');
}

$serviceId = 0;
foreach (['serviceid', 'id', 'sid'] as $k) {
    if (!empty($_REQUEST[$k])) { $serviceId = (int) $_REQUEST[$k]; break; }
}
if (!$serviceId) {
    ev_err('缺少服务 ID', 'SERVICE_ID_REQUIRED');
}

$service = Capsule::table('tblhosting')->where('id', $serviceId)->first();
if (!$service) {
    ev_err('服务不存在', 'SERVICE_NOT_FOUND');
}
// 租户隔离：非管理员只能访问自己的服务
if (!$isAdmin && (int) $service->userid !== $userid) {
    ev_err('无权访问此服务', 'ACCESS_DENIED');
}
if (empty($service->server)) {
    ev_err('该服务未绑定服务器', 'NO_SERVER');
}

$params = [
    'serviceid'  => $serviceId,
    'userid'     => (int) $service->userid,
    'serverid'   => (int) $service->server,
    'pid'        => (int) $service->packageid,
    'domain'     => $service->domain,
    'username'   => $service->username,
    'password'   => function_exists('decrypt') ? @decrypt($service->password) : $service->password,
    'status'     => $service->domainstatus,
];
if (!empty($service->configoptions)) {
    $params['configoptions'] = (array) json_decode($service->configoptions, true);
}

function ev_hostid($serviceId, $productId)
{
    $field = Capsule::table('tblcustomfields')
        ->where('relid', $productId)->where('fieldname', 'hostid')->first();
    if (!$field) { return ''; }
    $val = Capsule::table('tblcustomfieldsvalues')
        ->where('fieldid', $field->id)->where('relid', $serviceId)->first();
    return $val ? trim((string) $val->value) : '';
}

$hostid = ev_hostid($serviceId, (int) $service->packageid);
if ($hostid === '') {
    ev_err('该服务尚未开通实例', 'NOT_PROVISIONED');
}

try {
    $api = new EyvesCloud($params);
    // hostid 可能是数字 ID / UUID / 实例名，统一解析
    $ref = $hostid;
    try {
        $probe = $api->instance($ref);
    } catch (Throwable $e) {
        $found = $api->findInstanceByName($hostid);
        if (!$found || empty($found['id'])) {
            ev_err('面板中找不到该实例：' . $hostid, 'INSTANCE_NOT_FOUND');
        }
        $ref = (string) $found['id'];
    }
} catch (EyvesCloudException $e) {
    ev_err('无法连接面板：' . $e->getMessage(), 'PANEL_UNREACHABLE');
}

/** 取当前实例统一模型（带 caps） */
function ev_model(EyvesCloud $api, $ref, $withSub = false)
{
    $raw = $api->instance($ref);
    return EyvesMapper::instance($raw);
}

/** 写操作前的策略预检（与面板策略一致，提前给出友好提示） */
function ev_guard_write(EyvesCloud $api, $ref, $what)
{
    $m = ev_model($api, $ref);
    if (!empty($m['suspended'])) {
        ev_err('实例已挂起（欠费停机），不允许' . $what, 'SUSPENDED');
    }
    if (!empty($m['locked'])) {
        ev_err('实例已锁定，不允许' . $what, 'LOCKED');
    }
    return $m;
}

$action = isset($_REQUEST['action']) ? trim((string) $_REQUEST['action']) : 'getInfo';

/* ---------------------------------------------------------------------
 * 动作路由
 * ------------------------------------------------------------------- */

try {
    switch ($action) {

        /* -------- 基本信息 -------- */
        case 'getInfo': {
            $m = ev_model($api, $ref);
            $m['server_url']  = $api->baseUrl();
            $m['images']      = EyvesMapper::imageOptions($api->images($m['runtime']), $m['runtime']);
            ev_ok($m);
        }

        case 'getStatus': {
            $m = ev_model($api, $ref);
            ev_ok([
                'id'      => $m['id'],
                'status'  => $m['status'],
                'raw'     => $m['status_raw'],
                'runtime' => $m['runtime'],
                'traffic' => $m['traffic'],
                'caps'    => $m['caps'],
            ]);
        }

        /* -------- 页签数据 -------- */
        case 'getTabData': {
            $tab = isset($_REQUEST['tab']) ? trim((string) $_REQUEST['tab']) : 'base';
            $m   = ev_model($api, $ref);
            $out = ['caps' => $m['caps'], 'runtime' => $m['runtime']];

            switch ($tab) {
                case 'base':
                    $out['instance'] = $m;
                    break;

                case 'monitor':
                    $range = isset($_REQUEST['range']) ? (string) $_REQUEST['range'] : '1h';
                    $out['metrics'] = $api->metrics($m['id'], $range);
                    $out['usage']   = $api->usage($m['id']);
                    $out['traffic'] = $m['traffic'];
                    break;

                case 'network':
                    $out['instance']    = $m;
                    $out['port_map']    = $api->portMappings($m['id']);
                    $out['port_limit']  = $m['port_limit'];
                    $out['port_used']   = count($out['port_map']);
                    $out['can_edit']    = empty($m['suspended']) && empty($m['locked']);
                    break;

                case 'portmap':
                    $ports = $api->portMappings($m['id']);
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
                    $out['ports'] = $norm;
                    $out['limit'] = $m['port_limit'];
                    $out['used']  = count($norm);   // 以实际列表长度为准，不用实例视图里的计数字段
                    break;

                case 'snapshots':
                    $out['snapshots']      = EyvesMapper::snapshotList($api->snapshots($m['id']));
                    $out['snapshot_limit'] = $m['snapshot_limit'];
                    $out['can_create']     = $m['caps']['snapshot'] && empty($m['suspended']) && empty($m['locked']);
                    break;

                case 'backups':
                    $out['backups']    = EyvesMapper::backupList($api->backups($m['id']));
                    $out['can_create'] = $m['caps']['backup'] && empty($m['suspended']) && empty($m['locked']);
                    break;

                case 'securitys':   // 安全组（KVM/LXC 通用）
                    $out['groups']     = $api->securityGroups();
                    $out['attached']   = $api->instanceSecurityGroups($m['id']);
                    $out['can_edit']   = empty($m['suspended']) && empty($m['locked']);
                    break;

                case 'sshkey':
                    $out['keys'] = $api->sshKeys();
                    break;

                case 'drive':
                    // 面板为单块数据盘，只支持扩容
                    $out['instance']       = $m;
                    $out['data_disk_gb']   = $m['data_disk_gb'];
                    $out['can_resize']     = empty($m['suspended']) && empty($m['locked']);
                    break;

                case 'password':
                    // 与面板一致：改密属管理恢复路径，挂起态仍允许
                    $out['can_reset'] = true;
                    break;

                case 'crons':
                    $sched = isset($m['snapshot_schedule']) && is_array($m['snapshot_schedule'])
                        ? $m['snapshot_schedule'] : [];
                    $out['schedule'] = [
                        'enabled'        => !empty($sched['enabled']),
                        'interval_hours' => isset($sched['interval_hours']) ? (int) $sched['interval_hours'] : 0,
                        'time'           => isset($sched['time']) ? (string) $sched['time'] : '',
                        'last_run'       => isset($sched['last_run']) ? (string) $sched['last_run'] : '',
                        'next_run'       => isset($sched['next_run']) ? (string) $sched['next_run'] : '',
                    ];
                    break;

                case 'iso':
                    // 仅 KVM：能力位为 false 时前端不渲染该页签
                    if (empty($m['caps']['iso_mount'])) {
                        ev_err('该实例的虚拟化类型不支持 ISO 挂载', 'CAPABILITY_UNSUPPORTED');
                    }
                    $out['isos']          = $api->isoImages();
                    $out['rescue_enabled'] = $m['rescue_enabled'];
                    break;

                case 'tasks':
                    $out['tasks'] = EyvesMapper::taskList($api->tasks(['container' => $m['name']]));
                    break;

                case 'setting':
                    $out['instance'] = $m;
                    break;

                default:
                    ev_err('未知页签：' . $tab, 'UNKNOWN_TAB');
            }
            ev_ok($out);
        }

        /* -------- 控制台票据（LXC→SSH / KVM→VNC，自动分流） -------- */
        case 'getConsole': {
            $m = ev_model($api, $ref);
            $want = isset($_REQUEST['type']) ? strtolower(trim((string) $_REQUEST['type'])) : '';
            // 请求类型与运行时冲突时，以运行时为准并纠正（不让后端 400）
            if ($want === 'vnc' && empty($m['caps']['console_vnc'])) {
                $want = 'ssh';
            }
            if ($want === '') {
                $want = $m['console']['type'];
            }
            // 分流结果必须在失败时也告诉前端，否则界面上没法给出正确的提示文案
            // （「该实例未运行，无法打开 SSH 终端」vs「…VNC 控制台」）。
            try {
                $res = $api->console($m, $want);
            } catch (EyvesCloudException $e) {
                ev_json([
                    'status'  => 'error',
                    'code'    => $e->apiCode !== '' ? $e->apiCode : 'PANEL_ERROR',
                    'message' => $e->getMessage(),
                    'data'    => [
                        'effective_type' => $want,
                        'console'        => $want === 'vnc'
                            ? ['type' => 'vnc', 'label' => 'VNC 控制台', 'proto' => 'noVNC']
                            : ['type' => 'ssh', 'label' => 'SSH 终端', 'proto' => 'WebSSH'],
                        'caps'   => $m['caps'],
                        'status' => $m['status'],
                    ],
                ]);
            }
            $res['caps'] = $m['caps'];
            $res['effective_type'] = $want;
            $res['console'] = $want === 'vnc'
                ? ['type' => 'vnc', 'label' => 'VNC 控制台', 'proto' => 'noVNC']
                : ['type' => 'ssh', 'label' => 'SSH 终端', 'proto' => 'WebSSH'];
            ev_ok($res);
        }

        /* -------- 电源 -------- */
        case 'power': {
            $act = isset($_REQUEST['op']) ? strtolower(trim((string) $_REQUEST['op'])) : '';
            $allow = ['start' => 1, 'stop' => 1, 'shutdown' => 1, 'restart' => 1,
                      'hard-stop' => 1, 'hard-restart' => 1];
            if (!isset($allow[$act])) {
                ev_err('不支持的电源操作：' . $act, 'BAD_ACTION');
            }
            // 开机/重启 走完整可用性预检；关机永远允许（自救路径）
            if (in_array($act, ['start', 'restart', 'hard-restart'], true)) {
                ev_guard_write($api, $ref, '执行该操作');
            }
            ev_ok($api->power($ref, $act));
        }

        /* -------- 重置密码 -------- */
        case 'resetPassword': {
            // 与面板策略一致：改密是恢复路径，挂起态也允许
            $pw = isset($_REQUEST['password']) ? (string) $_REQUEST['password'] : '';
            ev_ok($api->resetPassword($ref, $pw));
        }

        /* -------- 重装系统 -------- */
        case 'reinstall': {
            $m = ev_guard_write($api, $ref, '重装系统');
            $imageId = isset($_REQUEST['image']) ? trim((string) $_REQUEST['image']) : '';
            if ($imageId === '') {
                ev_err('请选择系统镜像', 'IMAGE_REQUIRED');
            }
            // 关键防呆：镜像必须属于该实例的运行时。
            // 「该运行时一个镜像都没有」同样要拦 —— 否则会一路打到面板，
            // 报出一个和配置无关的底层错误（Template is not enabled or downloaded）。
            $ids = array_column($api->images($m['runtime']), 'id');
            if (empty($ids)) {
                ev_err(sprintf('%s 运行时当前没有可用镜像，无法重装', strtoupper($m['runtime'])), 'NO_IMAGES');
            }
            if (!in_array($imageId, $ids, true)) {
                ev_err(sprintf('镜像 %s 不属于 %s 运行时', $imageId, strtoupper($m['runtime'])), 'RUNTIME_MISMATCH');
            }
            ev_ok($api->reinstall($ref, $imageId, 'password', isset($_REQUEST['password']) ? (string) $_REQUEST['password'] : ''));
        }

        /* -------- 快照 -------- */
        case 'createSnapshot': {
            $m = ev_guard_write($api, $ref, '创建快照');
            ev_ok($api->createSnapshot($m['id'], isset($_REQUEST['name']) ? (string) $_REQUEST['name'] : ''));
        }
        case 'restoreSnapshot': {
            $m = ev_guard_write($api, $ref, '还原快照');
            $sid = isset($_REQUEST['sid']) ? (string) $_REQUEST['sid'] : '';
            ev_ok($api->restoreSnapshot($m['id'], $sid));
        }
        case 'deleteSnapshot': {
            $sid = isset($_REQUEST['sid']) ? (string) $_REQUEST['sid'] : '';
            ev_ok($api->deleteSnapshot($ref, $sid));
        }

        /* -------- 备份 -------- */
        case 'createBackup': {
            $m = ev_guard_write($api, $ref, '创建备份');
            ev_ok($api->createBackup($m['id']));
        }
        case 'restoreBackup': {
            $m = ev_guard_write($api, $ref, '还原备份');
            ev_ok($api->restoreBackup(isset($_REQUEST['bid']) ? (string) $_REQUEST['bid'] : ''));
        }
        case 'deleteBackup': {
            $bid = isset($_REQUEST['bid']) ? (string) $_REQUEST['bid'] : '';
            ev_ok($api->deleteBackup($ref, $bid));
        }

        /* -------- 带宽调整 -------- */
        case 'updateBandwidth': {
            $m = ev_guard_write($api, $ref, '修改带宽');
            $body = [];
            foreach (['down_mbps', 'up_mbps'] as $k) {
                if (isset($_REQUEST[$k]) && $_REQUEST[$k] !== '') {
                    $body[$k] = (int) $_REQUEST[$k];
                }
            }
            if (empty($body)) {
                ev_err('未指定带宽', 'BAD_PARAM');
            }
            ev_ok($api->updateInstance($m['id'], $body));
        }

        /* -------- 数据盘扩容（只增不减，与面板语义一致）-------- */
        case 'resizeDataDisk': {
            $m = ev_guard_write($api, $ref, '调整数据盘');
            $want = isset($_REQUEST['data_disk_gb']) ? (float) $_REQUEST['data_disk_gb'] : 0;
            if ($want <= 0) {
                ev_err('请填写数据盘容量', 'BAD_PARAM');
            }
            if ($want < (float) $m['data_disk_gb']) {
                ev_err(sprintf('数据盘不支持缩小（当前 %.0f GB，请求 %.0f GB）', $m['data_disk_gb'], $want), 'SHRINK_NOT_ALLOWED');
            }
            ev_ok($api->updateInstance($m['id'], ['data_disk_gb' => $want]));
        }

        /* -------- 修改主机名 --------
           注意：面板 v2 的 PATCH /instances/{id} **不支持 name 字段**
           （可改字段仅 vcpu/cpu_percent/memory_mb/disk_gb/data_disk_gb/
           down_mbps/up_mbps/traffic_quota_gb/snapshot_limit/remark/
           expires_at/tenant/firewall_enabled）。
           改名只能走 v1 的 POST /containers/{id}/hostname，语义是
           **实时修改运行中系统内的 hostname**（LXC 走 lxc-attach，KVM 走
           virsh set-hostname 并回退 SSH），因此要求实例处于 running，
           且字符集被面板限制为 RFC1123 的 [a-z0-9-]。 */
        case 'rename': {
            $m = ev_guard_write($api, $ref, '修改主机名');
            $name = isset($_REQUEST['hostname']) ? strtolower(trim((string) $_REQUEST['hostname'])) : '';
            if ($name === '' || strlen($name) > 63 || !preg_match('/^[a-z0-9][a-z0-9-]*$/', $name)) {
                ev_err('主机名不合法：仅允许小写字母、数字和连字符，1-63 位', 'BAD_PARAM');
            }
            if ($m['status'] !== 'running') {
                ev_err('修改主机名需要实例处于运行状态', 'PRECONDITION_FAILED');
            }
            ev_ok($api->call('POST', '/api/v1/containers/' . rawurlencode($m['id']) . '/hostname', ['hostname' => $name]));
        }

        /* -------- 备注（PATCH 支持的字段之一）-------- */
        case 'setRemark': {
            $m = ev_guard_write($api, $ref, '修改备注');
            $remark = isset($_REQUEST['remark']) ? trim((string) $_REQUEST['remark']) : '';
            if (mb_strlen($remark) > 200) {
                ev_err('备注过长（上限 200 字符）', 'BAD_PARAM');
            }
            ev_ok($api->updateInstance($m['id'], ['remark' => $remark]));
        }

        /* -------- 重置流量 -------- */
        case 'resetTraffic': {
            // 与面板一致：重置流量属计费字段类操作，挂起态仍允许（管理恢复路径）
            ev_ok($api->call('POST', '/api/v1/containers/' . rawurlencode($ref) . '/traffic-reset'));
        }

        /* -------- 端口映射 -------- */
        case 'addPortMapping': {
            $m = ev_guard_write($api, $ref, '修改端口映射');
            ev_ok($api->createPortMapping($m['id'], [
                'internal_port' => isset($_REQUEST['internal_port']) ? (int) $_REQUEST['internal_port'] : 0,
                'external_port' => isset($_REQUEST['external_port']) ? (int) $_REQUEST['external_port'] : 0,
                'protocol'      => isset($_REQUEST['protocol']) && $_REQUEST['protocol'] === 'udp' ? 'udp' : 'tcp',
                'remark'        => isset($_REQUEST['remark']) ? (string) $_REQUEST['remark'] : '',
            ]));
        }
        case 'deletePortMapping': {
            $m = ev_guard_write($api, $ref, '修改端口映射');
            ev_ok($api->deletePortMapping($m['id'], (int) ($_REQUEST['index'] ?? 0)));
        }
        case 'randomPort': {
            ev_ok($api->randomPort($ref));
        }

        /* -------- 安全组 -------- */
        case 'attachSecurityGroups': {
            $m = ev_guard_write($api, $ref, '修改安全组');
            $ids = isset($_REQUEST['groups']) ? (array) $_REQUEST['groups'] : [];
            ev_ok($api->setInstanceSecurityGroups($m['id'], $ids));
        }

        /* -------- 救援模式（仅 KVM，能力位把关） -------- */
        case 'rescueEnter': {
            $m = ev_guard_write($api, $ref, '进入救援模式');
            if (empty($m['caps']['rescue'])) {
                ev_err('该实例的虚拟化类型不支持救援模式', 'CAPABILITY_UNSUPPORTED');
            }
            ev_ok($api->enterRescue($m['id'], isset($_REQUEST['iso_id']) ? (string) $_REQUEST['iso_id'] : ''));
        }
        case 'rescueExit': {
            $m = ev_guard_write($api, $ref, '退出救援模式');
            ev_ok($api->exitRescue($m['id']));
        }

        /* -------- 任务撤回 -------- */
        case 'cancelTask': {
            ev_ok($api->cancelTask((string) ($_REQUEST['taskid'] ?? '')));
        }

        /* -------- 能力矩阵（前端首屏可一次性拿到） -------- */
        case 'capabilities': {
            $m = ev_model($api, $ref);
            ev_ok([
                'runtime' => $m['runtime'],
                'caps'    => $m['caps'],
                'console' => $m['console'],
            ]);
        }

        default:
            ev_err('未知操作：' . $action, 'UNKNOWN_ACTION');
    }
} catch (EyvesCloudException $e) {
    ev_err($e->getMessage(), $e->apiCode !== '' ? $e->apiCode : 'PANEL_ERROR');
} catch (Throwable $e) {
    ev_err($e->getMessage(), 'INTERNAL_ERROR');
}
