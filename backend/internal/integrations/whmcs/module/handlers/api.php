<?php
/**
 * EYVESCLOUD WHMCS 模块 - 客户区 AJAX 入口
 *
 * 浏览器请求路径：
 *   /modules/servers/eyvescloud/handlers/api.php
 *
 * 请求方式：POST，application/x-www-form-urlencoded（也兼容 JSON 请求体）
 *   id    服务 ID（tblhosting.id）
 *   func  动作名，见 helpers.php 的 eyvescloud_dispatch()
 *   其余业务字段随请求一起提交
 *
 * 响应：统一 JSON {success: bool, message: string, data: object, debug?: array}
 *
 * 该入口直接暴露在 Web 上，因此必须自行完成 WHMCS 会话与归属校验：
 *   1. 引入 WHMCS 引导文件以取得会话、数据库与加密能力；
 *   2. 校验请求来源与目标服务的归属，非本人服务一律拒绝。
 */

/**
 * 输出 JSON 并结束请求。
 *
 * @param int   $httpCode
 * @param array $payload
 */
function eyvescloud_api_respond($httpCode, array $payload)
{
    http_response_code($httpCode);
    if (!headers_sent()) {
        header('Content-Type: application/json; charset=utf-8');
        header('X-Content-Type-Options: nosniff');
        header('Cache-Control: no-store, no-cache, must-revalidate');
    }
    echo json_encode($payload, JSON_UNESCAPED_UNICODE);
    exit;
}

/**
 * 载入 WHMCS 引导文件（init.php），兼容自定义目录结构。
 */
function eyvescloud_api_bootstrap()
{
    if (defined('WHMCS')) {
        return true;
    }

    $candidates = [];
    $envRoot = getenv('EYVESCLOUD_WHMCS_ROOT');
    if (is_string($envRoot) && $envRoot !== '') {
        $candidates[] = rtrim($envRoot, '/\\') . '/init.php';
    }
    // handlers/api.php -> eyvescloud -> servers -> modules -> WHMCS 根目录
    $candidates[] = dirname(__DIR__, 4) . '/init.php';
    $candidates[] = dirname(__DIR__, 3) . '/init.php';

    foreach ($candidates as $path) {
        if (is_file($path)) {
            require_once $path;
            if (defined('WHMCS')) {
                return true;
            }
        }
    }

    return false;
}

if (!eyvescloud_api_bootstrap()) {
    eyvescloud_api_respond(500, [
        'success' => false,
        'message' => '无法载入 WHMCS 引导文件（init.php），请确认模块目录结构未被改动。',
        'data'    => [],
    ]);
}

// 引导完成后才能引入助手层（helpers.php 要求 WHMCS 常量已定义）。
require_once __DIR__ . '/../helpers.php';

if (!class_exists('\WHMCS\Database\Capsule')) {
    eyvescloud_api_respond(500, [
        'success' => false,
        'message' => 'WHMCS 数据库组件不可用。',
        'data'    => [],
    ]);
}

// 仅接受 POST，避免 GET 被用于触发状态变更。
if (strtoupper((string)($_SERVER['REQUEST_METHOD'] ?? 'GET')) !== 'POST') {
    eyvescloud_api_respond(405, [
        'success' => false,
        'message' => '仅支持 POST 请求',
        'data'    => [],
    ]);
}

// 会话校验：必须是已登录的 WHMCS 客户（或后台管理员）。
$uid = isset($_SESSION['uid']) ? (int)$_SESSION['uid'] : 0;
$adminId = isset($_SESSION['adminid']) ? (int)$_SESSION['adminid'] : 0;
if ($uid <= 0 && $adminId <= 0) {
    eyvescloud_api_respond(403, [
        'success' => false,
        'message' => '登录状态已失效，请刷新页面后重试',
        'data'    => [],
    ]);
}

// 同源校验（纵深防御）：若浏览器带上了 Origin/Referer，其主机必须与当前站点一致。
$selfHost = '';
if (!empty($_SERVER['HTTP_HOST'])) {
    $selfHost = strtolower((string)parse_url('http://' . $_SERVER['HTTP_HOST'], PHP_URL_HOST));
}
$originHost = '';
foreach (['HTTP_ORIGIN', 'HTTP_REFERER'] as $headerKey) {
    if (empty($_SERVER[$headerKey])) {
        continue;
    }
    $candidate = strtolower((string)parse_url((string)$_SERVER[$headerKey], PHP_URL_HOST));
    if ($candidate !== '') {
        $originHost = $candidate;
        break;
    }
}
if ($selfHost !== '' && $originHost !== '' && $originHost !== $selfHost) {
    eyvescloud_api_respond(403, [
        'success' => false,
        'message' => '请求来源不被信任',
        'data'    => [],
    ]);
}

$input = eyvescloud_json_input();

// CSRF token 校验：客户会话下必须携带与 session 一致的模块 token
// （eyvescloud_csrf_token 生成，clientarea.tpl data-csrf 输出、api() 附带回传）。
// Origin/Referer 同源校验在前，但浏览器可省略这两个头，此处为强校验。
// 纯管理员会话（uid=0）走 WHMCS 后台自带的核心 token 防护，不在此强制。
if ($uid > 0) {
    $sessionToken = isset($_SESSION['eyvescloud_csrf']) && is_string($_SESSION['eyvescloud_csrf'])
        ? $_SESSION['eyvescloud_csrf'] : '';
    $postedToken = isset($input['token']) && is_string($input['token']) ? $input['token'] : '';
    if ($sessionToken === '' || $postedToken === '' || !hash_equals($sessionToken, $postedToken)) {
        eyvescloud_api_respond(403, [
            'success' => false,
            'message' => '安全校验失败（token 无效），请刷新页面后重试',
            'data'    => [],
        ]);
    }
}

$serviceId = 0;
// 服务 ID 只从专用键读取：'id' 保留给资源级操作（快照/备份）使用，
// 若把 'id' 当作服务 ID 回退，快照 ID 可能覆盖服务 ID 从而取错服务。
foreach (['serviceid', 'service_id', 'hostid'] as $key) {
    if (!empty($input[$key]) && is_numeric($input[$key])) {
        $serviceId = (int)$input[$key];
        break;
    }
}

$action = '';
foreach (['func', 'action'] as $key) {
    if (!empty($input[$key]) && is_string($input[$key])) {
        $action = trim($input[$key]);
        break;
    }
}
// 兼容部分调用直接以动作名作为键（例如 {"infoData": 1}）。
if ($action === '' && !empty($input)) {
    foreach (['infoData', 'natList', 'firewallList', 'snapshotList', 'backupList', 'isoList', 'reinstallTemplates', 'status'] as $candidate) {
        if (isset($input[$candidate])) {
            $action = $candidate;
            break;
        }
    }
}

if ($serviceId <= 0) {
    eyvescloud_api_respond(400, [
        'success' => false,
        'message' => '缺少服务 ID',
        'data'    => [],
    ]);
}
if ($action === '') {
    eyvescloud_api_respond(400, [
        'success' => false,
        'message' => '缺少 func 参数',
        'data'    => [],
    ]);
}

// 由数据库重建模块标准 $params，并校验归属。
$params = eyvescloud_service_params($serviceId);
if (empty($params)) {
    eyvescloud_api_respond(404, [
        'success' => false,
        'message' => '服务不存在',
        'data'    => [],
    ]);
}

$ownerId = (int)($params['userid'] ?? 0);
if ($adminId <= 0 && $ownerId !== $uid) {
    eyvescloud_api_respond(403, [
        'success' => false,
        'message' => '无权操作该服务',
        'data'    => [],
    ]);
}

// F1a WHMCS 侧收口：服务非 Active（Suspended/Terminated/Expired/...）时
// 拒绝状态变更类操作，查询类放行。防止欠费客户经 WHMCS 客户区按钮
// 绕过面板侧的 Suspended 拦截。管理员会话豁免（与 TC-03 设计一致）。
if ($adminId <= 0) {
    $domainStatus = strtoupper(trim((string)($params['domainstatus'] ?? '')));
    if ($domainStatus !== 'ACTIVE' && !in_array($action, [
        // 查询/只读操作白名单
        'info', 'infoData', 'infoajax', 'status',
        'natList', 'natData', 'firewallList', 'snapshotList', 'backupList',
        'isoList', 'reinstallTemplates', 'randomPort', 'random-port',
        'sync',
    ], true)) {
        eyvescloud_api_respond(403, [
            'success' => false,
            'message' => '当前服务状态（' . $domainStatus . '）不允许此操作，请续费或联系客服',
            'data'    => [],
        ]);
    }
}

// 把请求体并入 $_POST，保证 helpers 内的取值函数都能读到业务字段。
foreach ($input as $key => $value) {
    if (!isset($_POST[$key])) {
        $_POST[$key] = $value;
    }
}

$result = eyvescloud_dispatch($params, $action);
if (!is_array($result)) {
    $result = ['success' => false, 'message' => '操作未返回有效结果', 'data' => []];
}

eyvescloud_api_respond(200, $result);