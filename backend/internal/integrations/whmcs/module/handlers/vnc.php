<?php
$ws = isset($_GET['ws']) ? (string)$_GET['ws'] : (isset($_GET['amp;ws']) ? (string)$_GET['amp;ws'] : '');
$protocol = isset($_GET['protocol']) ? (string)$_GET['protocol'] : (isset($_GET['amp;protocol']) ? (string)$_GET['amp;protocol'] : '');
$container = isset($_GET['container']) ? (string)$_GET['container'] : (isset($_GET['amp;container']) ? (string)$_GET['amp;container'] : '');
$ticket = isset($_GET['ticket']) ? (string)$_GET['ticket'] : (isset($_GET['amp;ticket']) ? (string)$_GET['amp;ticket'] : '');

if ($protocol === '' && $ticket !== '') {
    // KVM VNC 子协议前缀为 eyvescloud-vnc-ticket.（与 WebSSH 的 eyvescloud-ticket. 不同）。
    $protocol = 'eyvescloud-vnc-ticket.' . $ticket;
}

// 从 WHMCS 配置解析「该服务所属面板主机」，并入可信列表。
// WebSSH 与 VNC 必须保持一致：面板与计费系统不同域名时，无需额外配置
// EYVESCLOUD_WS_ALLOW 也能使用控制台。归属校验见 helpers.php。
function vnc_bootstrap_whmcs()
{
    if (defined('WHMCS')) {
        return true;
    }
    $candidates = array();
    $envRoot = getenv('EYVESCLOUD_WHMCS_ROOT');
    if (is_string($envRoot) && $envRoot !== '') {
        $candidates[] = rtrim($envRoot, '/\\') . '/init.php';
    }
    // handlers/vnc.php -> eyvescloud -> servers -> modules -> WHMCS 根目录
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

function vnc_configured_panel_hosts()
{
    $hosts = array();
    $serviceId = isset($_GET['id']) ? (int)$_GET['id'] : 0;
    if ($serviceId <= 0 || !vnc_bootstrap_whmcs()) {
        return $hosts;
    }
    // helpers.php 要求 WHMCS 常量已定义，因此只能放在 bootstrap 之后引入。
    if (!function_exists('eyvescloud_console_allowed_hosts')) {
        require_once dirname(__DIR__) . '/helpers.php';
    }
    if (!function_exists('eyvescloud_console_allowed_hosts')) {
        return $hosts;
    }
    foreach (eyvescloud_console_allowed_hosts($serviceId) as $h) {
        $hosts[] = $h;
    }
    return $hosts;
}

// 可信主机列表：当前请求域名 + 该服务配置的面板域名 + 环境变量 EYVESCLOUD_WS_ALLOW（逗号分隔）。
function vnc_allowed_hosts()
{
    $hosts = array();
    $self = isset($_SERVER['HTTP_HOST']) ? trim($_SERVER['HTTP_HOST']) : '';
    if ($self !== '') {
        $p = parse_url('http://' . $self);
        if ($p && !empty($p['host'])) {
            $hosts[] = strtolower($p['host']);
        }
    }
    foreach (vnc_configured_panel_hosts() as $h) {
        $hosts[] = $h;
    }
    $env = getenv('EYVESCLOUD_WS_ALLOW') ?: '';
    foreach (explode(',', $env) as $h) {
        $h = strtolower(trim($h));
        if ($h !== '') {
            $hosts[] = $h;
        }
    }
    return $hosts;
}

function vnc_reserved_ip($ip)
{
    return filter_var($ip, FILTER_VALIDATE_IP, FILTER_FLAG_NO_PRIV_RANGE | FILTER_FLAG_NO_RES_RANGE) === false;
}

// 校验 WebSocket 目标，阻止把本页面用作任意内网/公网目标的反向代理。
function vnc_validate_target($ws)
{
    $parts = parse_url($ws);
    if (!$parts || empty($parts['scheme']) || empty($parts['host'])) {
        return 'Invalid WebVNC target URL';
    }
    $scheme = strtolower($parts['scheme']);
    if ($scheme !== 'ws' && $scheme !== 'wss') {
        return 'WebVNC target must use ws:// or wss://';
    }
    $target = strtolower(trim($parts['host'], '[]'));
    foreach (vnc_allowed_hosts() as $h) {
        if ($h !== '' && $target === $h) {
            return ''; // 可信主机放行
        }
    }
    if (filter_var($target, FILTER_VALIDATE_IP) !== false && vnc_reserved_ip($target)) {
        return 'WebVNC target is not allowed (private/loopback/reserved)';
    }
    return 'WebVNC target host is not in the allowlist';
}

// 缺少参数时先返回 400，避免把「参数缺失」误报为 403 目标不可信。
if ($ws === '') {
    http_response_code(400);
    header('Content-Type: text/plain; charset=utf-8');
    echo "Missing WebVNC parameters\n";
    echo "Received query: " . ($_SERVER['QUERY_STRING'] ?? '') . "\n";
    exit;
}

// 安全校验：只允许转发到可信主机，避免把该页面当作任意内网/公网目标的反向代理（SSRF）。
$wsTargetError = vnc_validate_target($ws);
if ($wsTargetError !== '') {
    http_response_code(403);
    header('Content-Type: text/plain; charset=utf-8');
    echo $wsTargetError . "\n";
    exit;
}
?>
<!doctype html>
<html lang="zh-CN">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>WebVNC</title>
    <style>
        html,body{height:100%;margin:0;background:#0b1020;color:#e5e7eb;font-family:Consolas,Menlo,monospace}
        .bar{height:44px;display:flex;align-items:center;gap:12px;padding:0 14px;background:#111827;border-bottom:1px solid #243047}
        .dot{width:9px;height:9px;border-radius:50%;background:#f59e0b}
        .dot.ok{background:#22c55e}.dot.err{background:#ef4444}
        .title{font-size:14px;color:#cbd5e1;flex:1}
        .tools{display:flex;align-items:center;gap:8px;font-size:12px;color:#94a3b8;flex-wrap:wrap;justify-content:flex-end}
        .tools button{height:26px;border:1px solid #334155;background:#0f172a;color:#cbd5e1;border-radius:4px;padding:0 8px;cursor:pointer}
        #screen{height:calc(100% - 44px);box-sizing:border-box;background:#050505;overflow:hidden}
        #screen>div,#screen canvas{width:100%!important;height:100%!important}
        .hint{position:absolute;left:14px;bottom:12px;color:#94a3b8;font-size:13px}
    </style>
</head>
<body>
<div class="bar">
    <span id="state" class="dot"></span>
    <span class="title">WebVNC <?php echo htmlspecialchars($container, ENT_QUOTES, 'UTF-8'); ?></span>
    <span class="tools">
        <button id="cad" type="button">Ctrl+Alt+Del</button>
        <button id="reconnect" type="button">重新连接</button>
    </span>
</div>
<div id="screen"></div>
<div class="hint" id="hint">正在连接 KVM VNC 控制台...</div>

<script type="module">
(function(){
    var wsUrl = <?php echo json_encode($ws, JSON_UNESCAPED_SLASHES); ?>;
    var protocol = <?php echo json_encode($protocol, JSON_UNESCAPED_SLASHES); ?>;
    var ticket = <?php echo json_encode($ticket, JSON_UNESCAPED_SLASHES); ?>;
    var screen = document.getElementById('screen');
    var state = document.getElementById('state');
    var hint = document.getElementById('hint');
    var rfb = null;

    // noVNC 以 ES Module 发布，通过 CDN 动态加载；失败时给出明确提示。
    var NOVNC_URL = 'https://cdn.jsdelivr.net/npm/@novnc/novnc@1.5.0/lib/rfb.js';

    function setState(cls, text) {
        state.className = 'dot ' + cls;
        if (text) hint.textContent = text;
    }

    function websocketProtocolValue(value) {
        value = String(value || '');
        return /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(value) ? value : '';
    }

    async function connect() {
        if (rfb) {
            try { rfb.disconnect(); } catch (e) {}
            rfb = null;
        }
        screen.innerHTML = '';
        setState('', '正在连接 KVM VNC 控制台...');

        var RFB;
        try {
            var mod = await import(NOVNC_URL);
            RFB = mod && mod.default ? mod.default : mod;
        } catch (e) {
            setState('err', '无法加载 noVNC 组件，请检查网络或 CDN 可用性。');
            return;
        }
        if (typeof RFB !== 'function') {
            setState('err', 'noVNC 组件不可用。');
            return;
        }

        var protocolValue = websocketProtocolValue(protocol);
        var protocols = ['binary'];
        if (protocolValue) protocols.push(protocolValue);

        try {
            rfb = new RFB(screen, wsUrl, { wsProtocols: protocols });
            rfb.scaleViewport = true;
            rfb.resizeSession = false;
            rfb.focusOnClick = true;
            rfb.qualityLevel = 6;
            rfb.compressionLevel = 2;
            rfb.background = '#050505';
            rfb.addEventListener('connect', function(){ setState('ok', '已连接。'); });
            rfb.addEventListener('disconnect', function(event){
                var detail = event && event.detail;
                setState('err', detail && detail.clean === false
                    ? '连接已断开，请确认虚拟机正在运行且 VNC 控制台可用。'
                    : '连接已断开。');
            });
            rfb.addEventListener('securityfailure', function(){ setState('err', 'VNC 安全协商失败。'); });
            rfb.addEventListener('credentialsrequired', function(){ setState('err', '当前 VNC 控制台要求密码，暂不支持自动输入。'); });
        } catch (e) {
            setState('err', 'WebVNC 初始化失败：' + (e && e.message ? e.message : e));
        }
    }

    var cad = document.getElementById('cad');
    if (cad) {
        cad.addEventListener('click', function(){ if (rfb) rfb.sendCtrlAltDel(); });
    }
    var reconnect = document.getElementById('reconnect');
    if (reconnect) {
        reconnect.addEventListener('click', connect);
    }

    connect();
})();
</script>
</body>
</html>