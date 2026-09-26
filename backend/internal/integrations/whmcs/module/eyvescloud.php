<?php
/**
 * EYVESCLOUD - WHMCS 9.0 服务器开通模块
 *
 * 本模块把 WHMCS 的产品/服务生命周期映射到 EYVESCLOUD LXC/KVM 面板 API：
 *   开通、暂停、恢复、删除、开关机、重启、改密、变更套餐、同步、用量上报、客户区控制台。
 *
 * 所有与面板的底层交互与客户区 AJAX 逻辑都收敛在 helpers.php 中，本文件只负责实现
 * WHMCS 约定的模块入口函数。函数名必须以模块名 eyvescloud_ 作为前缀，否则 WHMCS 无法识别。
 *
 * WHMCS 服务器模块函数返回值约定：
 *   - 生命周期类函数（Create/Suspend/Unsuspend/Terminate/ChangePassword/ChangePackage/On/Off/Reboot）
 *     成功返回字符串 "success"，失败返回可读的错误消息字符串；
 *   - MetaData / ConfigOptions / TestConnection / ClientArea / UsageUpdate / 按钮 返回数组。
 */

if (!defined('WHMCS')) {
    die('This file cannot be accessed directly');
}

require_once __DIR__ . '/helpers.php';

/**
 * 模块元数据。
 */
function eyvescloud_MetaData()
{
    return [
        'DisplayName'               => 'EYVESCLOUD LXC/KVM 面板',
        'APIVersion'                => '1.1',
        'RequiresServer'            => true,
        'DefaultNonSSLPort'         => '80',
        'DefaultSSLPort'            => '443',
        'ServiceSingleSignOnLabel'  => '登录控制台',
        'AdminSingleSignOnLabel'    => '登录 EYVESCLOUD 面板',
    ];
}

/**
 * 产品配置项。顺序即 WHMCS configoption1..24 的映射顺序，与 helpers.php 的
 * eyvescloud_option_definitions() 保持同源，避免两处顺序不一致导致取值错位。
 *
 * 返回值必须为「关联数组」：WHMCS 9.0 规定外层键 = 配置项名称（用于内部索引），
 * 字段显示名放在 FriendlyName 中。这里直接用内部键（virtualization 等）作外层键。
 */
function eyvescloud_ConfigOptions()
{
    $options = [];
    foreach (eyvescloud_option_definitions() as $definition) {
        $option = [
            'FriendlyName' => $definition['label'],
            'Type'         => $definition['type'],
            'Description'  => $definition['description'] ?? '',
            'Default'      => $definition['default'] ?? '',
        ];

        if (!empty($definition['options']) && is_array($definition['options'])) {
            // WHMCS 支持把 Options 写成关联数组：键 = 写入 configoptionN 的值，值 = 下拉显示文案。
            // 这样存储的是 lxc/kvm 这类内部值，而不是显示标签，避免后续判断被标签污染。
            $option['Options'] = $definition['options'];
        }
        if (isset($definition['size'])) {
            $option['Size'] = (int)$definition['size'];
        }
        if (isset($definition['rows'])) {
            $option['Rows'] = (int)$definition['rows'];
        }
        if (isset($definition['cols'])) {
            $option['Cols'] = (int)$definition['cols'];
        }

        $options[$definition['key']] = $option;
    }
    return $options;
}

/* -------------------------------------------------------------------------
 * 服务器连接测试
 * ---------------------------------------------------------------------- */

/**
 * 测试与 EYVESCLOUD 面板的连通性（WHMCS 8+ 使用 TestConnection）。
 *
 * @param array $params
 * @return array{success:bool,error:string}
 */
function eyvescloud_TestConnection(array $params)
{
    $res = eyvescloud_request($params, '/api/v1/dashboard', [], 'GET', 30);
    if (eyvescloud_success($res)) {
        return ['success' => true, 'error' => ''];
    }
    return ['success' => false, 'error' => eyvescloud_message($res, '连接 EYVESCLOUD 面板失败')];
}

/* -------------------------------------------------------------------------
 * 生命周期
 * ---------------------------------------------------------------------- */

/**
 * 开通实例。
 *
 * EYVESCLOUD 的开通是异步任务：POST 返回后容器可能仍在初始化。这里在返回前做有界
 * 轮询，尽力把 ssh_port / ssh_password / 公网 IP 写回 WHMCS 主机表。
 *
 * @param array $params
 * @return string "success" 或错误消息
 */
function eyvescloud_CreateAccount(array $params)
{
    $exists = eyvescloud_find_container($params);
    if (eyvescloud_success($exists)) {
        return '该实例已存在，不能重复开通';
    }

    $payload = eyvescloud_container_payload($params);
    if (empty($payload['template_id'])) {
        return '产品配置缺少「镜像/模板 ID」';
    }

    // 幂等键：同一主机重试开通时，面板返回既有容器而不是二次开通。
    $idemKey = 'container-create-' . eyvescloud_host_id($params);
    $res = eyvescloud_request($params, '/api/v1/containers', $payload, 'POST', 120, ['Idempotency-Key' => $idemKey]);
    if (!eyvescloud_success($res)) {
        $message = (string)eyvescloud_message($res, '');
        if (stripos($message, 'idempotent') === false && stripos($message, 'already exists') === false) {
            return eyvescloud_message($res, '开通失败');
        }
    }

    $hostId = eyvescloud_host_id($params);
    if ($hostId > 0 && class_exists('\WHMCS\Database\Capsule')) {
        try {
            \WHMCS\Database\Capsule::table('tblhosting')->where('id', $hostId)->update([
                'domainstatus' => 'Active',
                'username'     => 'root',
                'dedicatedip'  => eyvescloud_public_ipv4_from_routing($params) ?: eyvescloud_public_host($params),
            ]);
        } catch (\Throwable $e) {
            return '开通成功但写回计费系统数据库失败: ' . $e->getMessage();
        }
    }

    $wait = eyvescloud_wait_container_ready($params);
    if (!empty($wait['container'])) {
        eyvescloud_update_host_from_container($params, $wait['container']);
    }
    if (!empty($wait['failed'])) {
        return '容器已创建，但' . ($wait['msg'] ?? '异步任务失败');
    }

    return 'success';
}

/**
 * 暂停实例（欠费停机）：调用面板的 suspend 接口。相比单纯关机，suspend 会拒绝
 * start/restart 与 WebSSH/VNC，避免客户在欠费状态下自行重新开机。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_SuspendAccount(array $params)
{
    return eyvescloud_account_action($params, 'suspend', '挂起任务已提交');
}

/**
 * 恢复实例（复机）：解除挂起标记。不自动开机，交由客户或管理员决定。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_UnsuspendAccount(array $params)
{
    return eyvescloud_account_action($params, 'unsuspend', '解除挂起任务已提交');
}

/**
 * 删除实例。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_TerminateAccount(array $params)
{
    $res = eyvescloud_container_delete($params);
    return ($res['status'] ?? '') === 'success' ? 'success' : ($res['msg'] ?? '删除失败');
}

/* -------------------------------------------------------------------------
 * 密码 / 套餐
 * ---------------------------------------------------------------------- */

/**
 * 修改实例密码（WHMCS 通过 $params['password'] 传入新密码）。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_ChangePassword(array $params)
{
    $newPassword = $params['password'] ?? '';
    if (is_array($newPassword)) {
        $newPassword = reset($newPassword);
    }
    $newPassword = trim((string)$newPassword);
    if ($newPassword === '') {
        return '新密码不能为空';
    }

    $res = eyvescloud_reset_password($params, $newPassword);
    return ($res['status'] ?? '') === 'success' ? 'success' : ($res['msg'] ?? '重置密码失败');
}

/**
 * 变更套餐：同步资源限制、流量限制与到期时间。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_ChangePackage(array $params)
{
    $resource = eyvescloud_resource_limit($params);
    if (($resource['status'] ?? '') !== 'success') {
        return $resource['msg'] ?? '资源限制调整失败';
    }

    $traffic = eyvescloud_traffic_limit($params);
    if (($traffic['status'] ?? '') !== 'success') {
        return $traffic['msg'] ?? '流量限制调整失败';
    }

    $expiry = eyvescloud_set_expiry($params);
    if (($expiry['status'] ?? '') !== 'success') {
        return $expiry['msg'] ?? '到期时间同步失败';
    }

    return 'success';
}

/* -------------------------------------------------------------------------
 * 电源管理
 * ---------------------------------------------------------------------- */

function eyvescloud_On(array $params)
{
    return eyvescloud_account_action($params, 'start', '开机任务已提交');
}

function eyvescloud_Off(array $params)
{
    return eyvescloud_account_action($params, 'stop', '关机任务已提交');
}

/**
 * 硬关机：libvirt destroy / lxc-stop -k。不 guest-agent / acpi，直接 kill。
 * 适合 soft stop 超时或卡死场景。
 */
function eyvescloud_HardOff(array $params)
{
    return eyvescloud_account_action($params, 'destroy', '硬关机任务已提交（强制 kill）', 90);
}

/**
 * KVM Rescue Mode：挂载救援 ISO 并从 ISO 冷启动。
 * 请求体 image 可选（ISO 文件名或 ID），空时用面板默认救援 ISO。
 */
function eyvescloud_RescueMode(array $params)
{
    $isoId = $_POST['iso_id'] ?? $_GET['iso_id'] ?? $_POST['image'] ?? $_GET['image'] ?? '';
    $cid = eyvescloud_host_id($params);
    if ($cid <= 0) {
        return '无法解析实例编号';
    }
    $payload = ['enabled' => true];
    if ($isoId !== '') {
        $payload['iso_id'] = $isoId;
    }
    $res = eyvescloud_request($params, '/api/v1/containers/' . $cid . '?action=rescue', $payload, 'POST', 120);
    if (!eyvescloud_success($res)) {
        return eyvescloud_message($res, '进入救援模式失败（仅 KVM 支持）');
    }
    return 'success';
}

/**
 * 退出 KVM Rescue Mode：卸载 rescue ISO，恢复 HDD 启动优先级，冷启动。
 */
function eyvescloud_RescueExit(array $params)
{
    $cid = eyvescloud_host_id($params);
    if ($cid <= 0) {
        return '无法解析实例编号';
    }
    $res = eyvescloud_request($params, '/api/v1/containers/' . $cid . '?action=rescue', ['enabled' => false], 'POST', 120);
    if (!eyvescloud_success($res)) {
        return eyvescloud_message($res, '退出救援模式失败');
    }
    return 'success';
}

function eyvescloud_Reboot(array $params)
{
    return eyvescloud_account_action($params, 'restart', '重启任务已提交');
}

/**
 * VNC 控制台（KVM 专属）。拿到 ticket 后 WHMCS 可直接 iframe 打开。
 * 返回 "success" 或可读错误消息（WHMCS 约定）。
 */
function eyvescloud_VNC(array $params)
{
    $res = eyvescloud_vnc_ticket($params);
    if (($res['status'] ?? '') === 'success') {
        return 'success';
    }
    return $res['msg'] ?? 'VNC 票据创建失败（仅 KVM 支持）';
}

/**
 * ISO 挂载（KVM 专属）。请求体 iso_id 可从 $_POST/$_GET 取。
 */
function eyvescloud_ISOAttach(array $params)
{
    $res = eyvescloud_isoAttach($params);
    return ($res['status'] ?? '') === 'success' ? 'success' : ($res['msg'] ?? 'ISO 挂载失败');
}

/**
 * ISO 卸载。
 */
function eyvescloud_ISODetach(array $params)
{
    $res = eyvescloud_isoDetach($params);
    return ($res['status'] ?? '') === 'success' ? 'success' : ($res['msg'] ?? 'ISO 卸载失败');
}

/* -------------------------------------------------------------------------
 * 同步 / 用量
 * ---------------------------------------------------------------------- */

/**
 * 从面板拉取容器详情并写回 WHMCS 主机表，同时同步到期时间。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_Sync(array $params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data']) || !is_array($res['data'])) {
        return eyvescloud_message($res, '同步失败：未找到实例');
    }

    eyvescloud_update_host_from_container($params, $res['data']);
    eyvescloud_set_expiry($params);

    return 'success';
}

/**
 * WHMCS 定期用量上报。注意：WHMCS 的 UsageUpdate 是「按服务器」而非「按产品」触发，
 * 因此这里遍历该服务器上的全部 Active 服务，逐个拉取面板用量后写回 tblhosting。
 *
 * @param array $params 服务器级参数（serverid / serverip / serverhostname / ...）
 * @return void
 */
function eyvescloud_UsageUpdate(array $params)
{
    if (!class_exists('\WHMCS\Database\Capsule')) {
        return;
    }

    $serverId = (int)($params['serverid'] ?? 0);
    if ($serverId <= 0) {
        return;
    }

    $services = \WHMCS\Database\Capsule::table('tblhosting')
        ->where('server', $serverId)
        ->where('domainstatus', 'Active')
        ->get(['id']);

    foreach ($services as $service) {
        $serviceId = (int)($service->id ?? 0);
        if ($serviceId <= 0) {
            continue;
        }

        $serviceParams = eyvescloud_service_params($serviceId);
        if (empty($serviceParams)) {
            continue;
        }

        $usage = eyvescloud_container_usage($serviceParams);
        if (empty($usage['ok'])) {
            continue;
        }

        $bwusage = round($usage['total_bytes'] / 1048576, 2);
        $bwlimit = $usage['limit_gb'] > 0 ? round($usage['limit_gb'] * 1024, 2) : 0;

        try {
            \WHMCS\Database\Capsule::table('tblhosting')->where('id', $serviceId)->update([
                'bwusage'    => $bwusage,
                'bwlimit'    => $bwlimit,
                'lastupdate' => date('Y-m-d H:i:s'),
            ]);
        } catch (\Throwable $e) {
            // 单个服务写回失败不应中断整轮同步。
        }
    }
}

/* -------------------------------------------------------------------------
 * 客户区
 * ---------------------------------------------------------------------- */

/**
 * 客户区输出。返回 Smarty 模板与变量。
 *
 * @param array $params
 * @return array
 */
function eyvescloud_ClientArea(array $params)
{
    $options = eyvescloud_options($params);
    $virtualization = strtolower(trim((string)($options['virtualization'] ?? 'lxc')));

    $password = $params['password'] ?? '';
    if (is_array($password)) {
        $password = reset($password);
    }

    return [
        'templatefile' => 'templates/clientarea',
        'vars' => [
            'service_id'     => eyvescloud_host_id($params),
            'module_url'     => eyvescloud_module_url($params),
            'container_name' => eyvescloud_container_name($params),
            'virtualization' => $virtualization,
            'is_kvm'         => $virtualization === 'kvm' ? '1' : '0',
            'ssh_password'   => (string)$password,
        ],
    ];
}

/**
 * 客户区可调用的自定义函数白名单（键 = 显示名，值 = 去掉 eyvescloud_ 前缀的函数名）。
 *
 * 注意：客户区的交互式操作（开机/关机/重装/NAT/防火墙/快照/备份/ISO/控制台）默认通过
 * 模块自带的 AJAX 入口 handlers/api.php 完成，不依赖此白名单；这里声明后，
 * 也允许通过 WHMCS 标准的 modop=custom 方式触发这些动作，作为兼容路径。
 *
 * @return array<string,string>
 */
function eyvescloud_ClientAreaAllowedFunctions()
{
    return [
        '开机'          => 'On',
        '关机'          => 'Off',
        '重启'          => 'Reboot',
        '硬关机(强制)'  => 'HardOff',
        'KVM 救援模式'  => 'RescueMode',
        '退出救援模式'  => 'RescueExit',
        'ISO 挂载'      => 'ISOAttach',
        'ISO 卸载'      => 'ISODetach',
        'VNC 控制台'    => 'VNC',
        '同步状态'      => 'Sync',
        '重置流量'      => 'TrafficReset',
    ];
}

/**
 * 后台/服务器列表里的面板快捷入口。
 *
 * @param array $params
 * @return string HTML
 */
function eyvescloud_AdminLink(array $params)
{
    $base = eyvescloud_base_url($params);
    if ($base === '') {
        return '';
    }

    return '<a href="' . htmlspecialchars($base, ENT_QUOTES, 'UTF-8') . '" target="_blank" rel="noopener noreferrer">打开 EYVESCLOUD 面板</a>';
}

/**
 * 产品管理页入面板链接（WHMCS 要求返回链接、不能是表单）。
 *
 * @param array $params
 * @return string HTML
 */
function eyvescloud_LoginLink(array $params)
{
    return eyvescloud_AdminLink($params);
}

/**
 * 单点登录：从 WHMCS 后台直接跳转到 EYVESCLOUD 用户门户的实例详情页。
 *
 * @param array $params
 * @return array
 */
function eyvescloud_ServiceSingleSignOn(array $params)
{
    $container = [];
    eyvescloud_container_api_id($params, $container);
    $id = $container['id'] ?? ($container['name'] ?? eyvescloud_container_name($params));

    $url = eyvescloud_panel_container_url($params, $id);
    if ($url === '') {
        return ['success' => false, 'errorMsg' => '未配置面板地址'];
    }

    return ['success' => true, 'redirectTo' => $url];
}

/* -------------------------------------------------------------------------
 * 后台服务页 / 自定义按钮
 * ---------------------------------------------------------------------- */

/**
 * 后台服务页字段。展示实例实时状态与面板入口。
 *
 * @param array $params
 * @return array<string,string>
 */
function eyvescloud_AdminServicesTabFields(array $params)
{
    $res = eyvescloud_find_container($params);
    $container = (eyvescloud_success($res) && !empty($res['data']) && is_array($res['data'])) ? $res['data'] : [];

    if (empty($container)) {
        return [
            '实例状态' => '<span style="color:#b91c1c">无法获取实例信息：' . htmlspecialchars(eyvescloud_message($res, '面板未响应'), ENT_QUOTES, 'UTF-8') . '</span>',
        ];
    }

    $statusRaw = strtolower((string)($container['status'] ?? ''));
    $statusText = $statusRaw === 'running' ? '运行中' : (in_array($statusRaw, ['stopped', 'shutdown'], true) ? '已关机' : ($statusRaw !== '' ? $statusRaw : '未知'));

    $panelUrl = eyvescloud_panel_container_url($params, $container['id'] ?? ($container['name'] ?? ''));
    $panelLink = $panelUrl !== ''
        ? '<a href="' . htmlspecialchars($panelUrl, ENT_QUOTES, 'UTF-8') . '" target="_blank" rel="noopener noreferrer">打开面板控制台</a>'
        : '-';

    return [
        '实例名称' => htmlspecialchars((string)($container['name'] ?? eyvescloud_container_name($params)), ENT_QUOTES, 'UTF-8'),
        '运行状态' => htmlspecialchars($statusText, ENT_QUOTES, 'UTF-8'),
        'SSH 地址' => htmlspecialchars(eyvescloud_public_host($params, $container, true), ENT_QUOTES, 'UTF-8'),
        'SSH 端口' => htmlspecialchars((string)($container['ssh_port'] ?? '-'), ENT_QUOTES, 'UTF-8'),
        '到期时间' => htmlspecialchars((string)($container['expires_at'] ?? '-'), ENT_QUOTES, 'UTF-8'),
        '面板入口' => $panelLink,
    ];
}

/**
 * 后台自定义按钮：按钮标签 => 模块函数后缀（实际调用 eyvescloud_{后缀}）。
 *
 * @return array<string,string>
 */
function eyvescloud_AdminCustomButtonArray()
{
    return [
        '同步状态'     => 'Sync',
        '重置流量'     => 'TrafficReset',
        '硬关机(强制)' => 'HardOff',
        'KVM 救援模式' => 'RescueMode',
        '退出救援模式' => 'RescueExit',
        'ISO 挂载'     => 'ISOAttach',
        'ISO 卸载'     => 'ISODetach',
        'VNC 控制台'   => 'VNC',
    ];
}

/**
 * 后台按钮动作：重置实例流量。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_TrafficReset(array $params)
{
    $res = eyvescloud_traffic_reset($params);
    return ($res['status'] ?? '') === 'success' ? 'success' : ($res['msg'] ?? '流量重置失败');
}

/* -------------------------------------------------------------------------
 * 内部工具
 * ---------------------------------------------------------------------- */

/**
 * 把 helpers.php 中返回 {status,msg} 的操作结果转换为 WHMCS 生命周期函数要求的字符串。
 *
 * @param array  $params
 * @param string $action      面板动作（start/stop/restart）
 * @param string $successMsg  成功消息（用作失败时的回退文案）
 * @param int    $timeout
 * @return string
 */
function eyvescloud_account_action(array $params, $action, $successMsg, $timeout = 60)
{
    $res = eyvescloud_container_action($params, $action, $successMsg, $timeout);
    if (($res['status'] ?? '') === 'success') {
        return 'success';
    }
    return $res['msg'] ?? ($successMsg . '失败');
}