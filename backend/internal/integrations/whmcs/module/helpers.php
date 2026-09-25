<?php
/**
 * EYVESCLOUD WHMCS 服务器模块 - 共享助手层
 *
 * 该文件承载三类内容：
 *   1. WHMCS $params 适配器（serverhostname / serverport / serveraccesshash 等）；
 *   2. 与 EYVESCLOUD 面板交互的底层 API 封装（端点和载荷以已验证的魔方模块为权威契约）；
 *   3. 供客户区 AJAX 使用的统一分发器与结果规整器。
 *
 * 本文件被 eyvescloud.php 与 handlers/api.php 通过 require_once 复用，因此所有函数
 * 都必须使用 eyvescloud_ 前缀，避免与 WHMCS 核心函数冲突。
 */

if (!defined("WHMCS")) {
    die("This file cannot be accessed directly");
}

if (!defined('EYVESCLOUD_DEBUG')) {
    define('EYVESCLOUD_DEBUG', false);
}

/**
 * 调试日志（默认关闭，可通过 EYVESCLOUD_DEBUG 常量打开）。
 */
function eyvescloud_debug($message, $data = null)
{
    if (!EYVESCLOUD_DEBUG) {
        return;
    }
    $line = '[EYVESCLOUD-DEBUG] ' . $message;
    if ($data !== null) {
        $line .= ' | ' . json_encode($data, JSON_UNESCAPED_UNICODE);
    }
    error_log($line);
}

function eyvescloud_debug_entry($message, $data = null)
{
    return [
        'time'    => date('Y-m-d H:i:s'),
        'message' => $message,
        'data'    => $data,
    ];
}

/* -------------------------------------------------------------------------
 * 配置项顺序与适配
 * ---------------------------------------------------------------------- */

/**
 * 产品配置项的标准定义（顺序即 WHMCS configoption1..24 的映射顺序）。
 *
 * 这里作为“唯一事实来源”：eyvescloud_ConfigOptions() 据此生成 WHMCS 键值格式，
 * eyvescloud_options() 据此把 $params 还原成内部键名，避免两处顺序不一致。
 */
function eyvescloud_option_definitions()
{
    return [
        [
            'label'       => '虚拟化类型',
            'key'         => 'virtualization',
            'type'        => 'dropdown',
            'default'     => 'lxc',
            'description' => 'lxc 或 kvm',
            'options'     => ['lxc' => 'LXC', 'kvm' => 'KVM'],
        ],
        [
            'label'       => '镜像/模板 ID',
            'key'         => 'template_id',
            'type'        => 'text',
            'size'        => 40,
            'default'     => 'alpine-3.21',
            'description' => 'EYVESCLOUD 模板 ID，例如 alpine-3.21、debian-bookworm、ubuntu-jammy 或已启用的 KVM 镜像 ID',
        ],
        [
            'label'       => 'CPU 核心',
            'key'         => 'vcpu',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '1',
            'description' => 'vCPU 数量，KVM 必须为整数',
        ],
        [
            'label'       => 'CPU 百分比',
            'key'         => 'cpu_percent',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '0',
            'description' => 'CPU 使用率限制，0 表示不额外限制',
        ],
        [
            'label'       => '内存 MB',
            'key'         => 'ram_mb',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '512',
            'description' => '容器内存，单位 MB',
        ],
        [
            'label'       => '硬盘 GB',
            'key'         => 'disk_gb',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '5',
            'description' => '系统盘大小，单位 GB（支持 0.5、0.75、1、5 等浮点数）',
        ],
        [
            'label'       => '带宽 Mbps',
            'key'         => 'network_bw_mbps',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '100',
            'description' => '网络带宽限制，0 表示不限制',
        ],
        [
            'label'       => '流量模式',
            'key'         => 'traffic_mode',
            'type'        => 'dropdown',
            'default'     => 'total',
            'description' => 'total=总流量，in_out=分别限制入/出方向',
            'options'     => ['total' => '总流量', 'in_out' => '入/出分开'],
        ],
        [
            'label'       => '月流量 GB',
            'key'         => 'monthly_traffic_gb',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '100',
            'description' => 'total 模式下的月流量限制，0 表示不限制',
        ],
        [
            'label'       => '入站流量 GB',
            'key'         => 'traffic_in_gb',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '0',
            'description' => 'in_out 模式下入站流量限制，0 表示不限制',
        ],
        [
            'label'       => '出站流量 GB',
            'key'         => 'traffic_out_gb',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '0',
            'description' => 'in_out 模式下出站流量限制，0 表示不限制',
        ],
        [
            'label'       => 'IO 速度 MB/s',
            'key'         => 'io_speed_mbps',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '0',
            'description' => '磁盘 IO 限制，0 表示不限制',
        ],
        [
            'label'       => '分配 NAT',
            'key'         => 'assign_nat',
            'type'        => 'dropdown',
            'default'     => 'true',
            'description' => '开通时是否分配 NAT 端口映射',
            'options'     => ['true' => '启用', 'false' => '禁用'],
        ],
        [
            'label'       => 'NAT 端口数量',
            'key'         => 'port_mapping_count',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '2',
            'description' => '开通时分配的端口映射数量，最小 2',
        ],
        [
            'label'       => '快照配额',
            'key'         => 'snapshot_limit',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '3',
            'description' => '每台实例允许保留的快照数量',
        ],
        [
            'label'       => '额外端口',
            'key'         => 'extra_ports',
            'type'        => 'text',
            'size'        => 40,
            'default'     => '',
            'description' => '逗号分隔的容器端口，例如 80,443',
        ],
        [
            'label'       => '自动公网 IPv4',
            'key'         => 'assign_ipv4',
            'type'        => 'dropdown',
            'default'     => 'false',
            'description' => '开通时是否从 EYVESCLOUD 公网 IPv4 池分配独立 IPv4',
            'options'     => ['true' => '启用', 'false' => '禁用'],
        ],
        [
            'label'       => '公网 IPv4 数量',
            'key'         => 'ipv4_count',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '1',
            'description' => '自动分配公网 IPv4 的数量，通常填写 1',
        ],
        [
            'label'       => '自动 IPv6',
            'key'         => 'assign_ipv6',
            'type'        => 'dropdown',
            'default'     => 'false',
            'description' => '开通时自动分配 IPv6',
            'options'     => ['true' => '启用', 'false' => '禁用'],
        ],
        [
            'label'       => 'IPv6 数量',
            'key'         => 'ipv6_count',
            'type'        => 'text',
            'size'        => 10,
            'default'     => '1',
            'description' => '自动分配 IPv6 的数量，通常填写 1',
        ],
        [
            'label'       => 'SSH 鉴权模式',
            'key'         => 'ssh_auth_mode',
            'type'        => 'dropdown',
            'default'     => 'auto_password',
            'description' => 'auto_password=自动生成密码，password=使用指定密码，key=使用 SSH 公钥',
            'options'     => ['auto_password' => '自动密码', 'password' => '指定密码', 'key' => 'SSH 公钥'],
        ],
        [
            'label'       => '指定 SSH 密码',
            'key'         => 'ssh_password',
            'type'        => 'password',
            'size'        => 40,
            'default'     => '',
            'description' => 'SSH 鉴权模式为 password 时使用；其他模式留空',
        ],
        [
            'label'       => 'SSH 公钥',
            'key'         => 'ssh_public_key',
            'type'        => 'textarea',
            'rows'        => 4,
            'cols'        => 60,
            'default'     => '',
            'description' => 'SSH 鉴权模式为 key 时使用；填写完整 public key',
        ],
        [
            'label'       => '同步到期时间',
            'key'         => 'sync_expiry',
            'type'        => 'dropdown',
            'default'     => 'true',
            'description' => '开通/续费时把计费系统到期日期同步到 EYVESCLOUD，格式会转换为 YYYY-MM-DD',
            'options'     => ['true' => '启用', 'false' => '禁用'],
        ],
    ];
}

/**
 * 返回按顺序排列的内部键名列表（对应 configoption1..24）。
 *
 * @return string[]
 */
function eyvescloud_option_keys()
{
    $keys = [];
    foreach (eyvescloud_option_definitions() as $definition) {
        $keys[] = $definition['key'];
    }
    return $keys;
}

/**
 * 返回 “配置项中文标签 => 内部键名” 映射，用于兼容 $params['configoptions'] 按标签索引的情况。
 *
 * @return array<string,string>
 */
function eyvescloud_option_labels()
{
    $map = [];
    foreach (eyvescloud_option_definitions() as $definition) {
        $map[$definition['label']] = $definition['key'];
    }
    return $map;
}

/**
 * 把 WHMCS 的 $params 还原成 “内部键名 => 值” 数组。
 *
 * 优先读取 configoption1..24（按 eyvescloud_option_keys() 顺序），随后按标签合并
 * $params['configoptions']，最后回填默认值。所有内部逻辑都应通过该函数读取配置项，
 * 这样无论 WHMCS 的索引方式如何变化都能正确工作。
 *
 * @param array $params
 * @return array<string,string>
 */
function eyvescloud_options($params)
{
    $values = [];
    foreach (eyvescloud_option_keys() as $index => $key) {
        $value = $params['configoption' . ($index + 1)] ?? '';
        if (is_array($value)) {
            $value = reset($value);
        }
        $values[$key] = $value;
    }

    if (!empty($params['configoptions']) && is_array($params['configoptions'])) {
        $labelMap = eyvescloud_option_labels();
        foreach ($params['configoptions'] as $label => $value) {
            if (!isset($labelMap[$label])) {
                continue;
            }
            if (is_array($value)) {
                $value = reset($value);
            }
            if ($value !== null && $value !== '') {
                $values[$labelMap[$label]] = $value;
            }
        }
    }

    foreach (eyvescloud_option_definitions() as $definition) {
        $key = $definition['key'];
        if (!isset($values[$key]) || $values[$key] === null || $values[$key] === '') {
            $values[$key] = $definition['default'];
        }
    }

    return $values;
}

/* -------------------------------------------------------------------------
 * WHMCS $params 适配：面板地址与 API Key
 * ---------------------------------------------------------------------- */

/**
 * 计算面板基础地址。
 *
 * 优先 serverhostname，其次 serverip；serversecure 为真时使用 https；serverport
 * 非空且未包含在 host 中时追加端口。同时保留魔方风格的 server_host / server_ip /
 * port / secure 作为回退，方便共用同一套代码。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_base_url($params)
{
    $explicit = $params['server_host'] ?? '';
    if (!empty($explicit)) {
        return rtrim($explicit, '/');
    }

    $host = $params['serverhostname'] ?? ($params['serverip'] ?? ($params['server_ip'] ?? ($params['ip'] ?? '')));
    $port = $params['serverport'] ?? ($params['port'] ?? '');
    $secure = $params['serversecure'] ?? ($params['secure'] ?? '');
    $scheme = (!empty($secure) && (string)$secure !== '0') ? 'https' : 'http';

    $host = trim((string)$host);
    $port = trim((string)$port);

    if ($host === '') {
        return '';
    }

    if (stripos($host, 'http://') === 0 || stripos($host, 'https://') === 0) {
        $base = rtrim($host, '/');
    } else {
        $base = $scheme . '://' . $host;
    }

    // 仅当端口尚未出现在主机部分时才追加，避免出现 host:443:443。
    $hostPart = parse_url($base, PHP_URL_HOST) ?: $base;
    if ($port !== '' && strpos($hostPart, ':') === false) {
        $base .= ':' . $port;
    }

    return rtrim($base, '/');
}

/**
 * 解析面板 API Key：serveraccesshash 优先，其次 serverpassword。
 * 兼容魔方风格的 accesshash / server_password / password。
 *
 * @param array $params
 * @return string
 */
function eyvescloud_api_key($params)
{
    foreach (['serveraccesshash', 'serverpassword', 'accesshash', 'server_password', 'password'] as $key) {
        if (!empty($params[$key])) {
            $value = $params[$key];
            if (is_array($value)) {
                $value = reset($value);
            }
            return trim((string)$value);
        }
    }
    return '';
}

/**
 * 站点根地址（用于拼装模块公开 URL）。优先 WHMCS SystemURL 设置。
 *
 * @return string
 */
function eyvescloud_site_base_url()
{
    if (class_exists('\WHMCS\Config\Setting')) {
        try {
            $url = \WHMCS\Config\Setting::getValue('SystemURL');
            if (!empty($url)) {
                return rtrim((string)$url, '/');
            }
        } catch (\Throwable $e) {
            eyvescloud_debug('SystemURL lookup failed', $e->getMessage());
        }
    }

    if (!empty($GLOBALS['CONFIG']['SystemURL'])) {
        return rtrim((string)$GLOBALS['CONFIG']['SystemURL'], '/');
    }

    $scheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $host = $_SERVER['HTTP_HOST'] ?? '';
    return $host !== '' ? $scheme . '://' . $host : '';
}

/**
 * 模块公开目录 URL（以 / 结尾），例如 https://billing.example.com/modules/servers/eyvescloud/
 *
 * @param array $params 未使用，仅为保持签名兼容
 * @return string
 */
function eyvescloud_module_url($params = [])
{
    return eyvescloud_site_base_url() . '/modules/servers/eyvescloud/';
}

/**
 * 面板控制台入口地址（用于后台/SSO 跳转）。
 * 用户门户路由固定为 /user，详情页为 /user/container/{id}。
 *
 * @param array  $params
 * @param string $containerIdOrName
 * @return string
 */
function eyvescloud_panel_container_url($params, $containerIdOrName = '')
{
    $base = eyvescloud_base_url($params);
    if ($base === '') {
        return '';
    }
    if ($containerIdOrName === '') {
        return $base . '/user';
    }
    return $base . '/user/container/' . rawurlencode((string)$containerIdOrName);
}

/* -------------------------------------------------------------------------
 * 加密辅助
 * ---------------------------------------------------------------------- */

/**
 * 解密 WHMCS 存储的敏感字段（tblservers.password / accesshash 等）。
 *
 * 在模块被 WHMCS 调用时，$params 中的值已经是明文，无需调用本函数；
 * 仅在 handlers/api.php 直接读取数据库构建 $params 时使用。
 *
 * @param mixed $value
 * @return string
 */
function eyvescloud_decrypt($value)
{
    $value = is_array($value) ? (string)reset($value) : (string)$value;
    if ($value === '') {
        return '';
    }

    if (function_exists('decrypt')) {
        try {
            $plain = decrypt($value);
            if (is_string($plain) && $plain !== '') {
                return $plain;
            }
        } catch (\Throwable $e) {
            eyvescloud_debug('decrypt() failed', $e->getMessage());
        }
    }

    if (function_exists('localAPI')) {
        try {
            $res = localAPI('DecryptPassword', ['password2' => $value]);
            if (is_array($res) && strtolower((string)($res['result'] ?? '')) === 'success') {
                if (!empty($res['password'])) {
                    return (string)$res['password'];
                }
                if (!empty($res['password2'])) {
                    return (string)$res['password2'];
                }
            }
        } catch (\Throwable $e) {
            eyvescloud_debug('DecryptPassword failed', $e->getMessage());
        }
    }

    return $value;
}

/**
 * 加密敏感字段以便写回 WHMCS 数据库。无可用加密函数时按原文写入。
 *
 * @param string $password
 * @return string
 */
function eyvescloud_store_password($password)
{
    $password = (string)$password;
    if ($password === '') {
        return '';
    }
    if (function_exists('encrypt')) {
        try {
            $encrypted = encrypt($password);
            if (is_string($encrypted) && $encrypted !== '') {
                return $encrypted;
            }
        } catch (\Throwable $e) {
            eyvescloud_debug('encrypt() failed', $e->getMessage());
        }
    }
    return $password;
}

/* -------------------------------------------------------------------------
 * HTTP 传输
 * ---------------------------------------------------------------------- */

/**
 * 调用 EYVESCLOUD 面板 API。
 *
 * 默认开启 TLS 证书校验；仅当 $params['insecure'] 为真（或产品配置项 insecure=1）
 * 时允许自签证书跳过校验。
 *
 * @param array  $params
 * @param string $endpoint     以 / 开头的接口路径
 * @param array  $data         请求体
 * @param string $method       HTTP 方法
 * @param int    $timeout      超时秒数
 * @param array  $extraHeaders 额外请求头（名 => 值）
 * @return array
 */
function eyvescloud_request($params, $endpoint, $data = [], $method = 'GET', $timeout = 30, $extraHeaders = [])
{
    $base = eyvescloud_base_url($params);
    if ($base === '') {
        return ['success' => false, 'message' => '未配置面板地址（serverhostname/serverip）', '_http_code' => 0];
    }

    $url = $base . $endpoint;
    $apiKey = eyvescloud_api_key($params);
    $method = strtoupper($method);

    $insecure = !empty($params['insecure']);
    if (!$insecure && !empty($params['configoptions']['insecure'])) {
        $insecure = eyvescloud_bool_option($params['configoptions']['insecure'], false);
    }

    $curl = curl_init();
    $headers = [
        'Content-Type: application/json',
        'Accept: application/json',
        'X-API-Key: ' . $apiKey,
        'Authorization: Bearer ' . $apiKey,
    ];
    foreach ((array)$extraHeaders as $name => $value) {
        $headers[] = $name . ': ' . $value;
    }

    $options = [
        CURLOPT_URL            => $url,
        CURLOPT_RETURNTRANSFER => true,
        CURLOPT_TIMEOUT        => $timeout,
        CURLOPT_CONNECTTIMEOUT => 10,
        CURLOPT_FOLLOWLOCATION => true,
        CURLOPT_CUSTOMREQUEST  => $method,
        CURLOPT_HTTPHEADER     => $headers,
        CURLOPT_SSL_VERIFYPEER => $insecure ? false : true,
        CURLOPT_SSL_VERIFYHOST => $insecure ? 0 : 2,
        CURLOPT_USERAGENT      => 'WHMCS-EYVESCLOUD',
    ];

    if ($method !== 'GET' && $data !== null) {
        $options[CURLOPT_POSTFIELDS] = json_encode($data, JSON_UNESCAPED_UNICODE);
    }

    curl_setopt_array($curl, $options);
    $body = curl_exec($curl);
    $errno = curl_errno($curl);
    $error = curl_error($curl);
    $httpCode = curl_getinfo($curl, CURLINFO_HTTP_CODE);
    curl_close($curl);

    eyvescloud_debug('request', ['url' => $url, 'method' => $method, 'http_code' => $httpCode, 'errno' => $errno]);

    if ($errno) {
        return ['success' => false, 'message' => 'CURL ERROR: ' . $error, '_http_code' => 0];
    }

    $decoded = json_decode((string)$body, true);
    if (!is_array($decoded)) {
        return ['success' => false, 'message' => '面板返回非 JSON 数据: ' . substr((string)$body, 0, 300), '_http_code' => $httpCode];
    }

    $decoded['_http_code'] = $httpCode;
    return $decoded;
}

function eyvescloud_request_debug($params, $endpoint, $data = [], $method = 'GET', $timeout = 30)
{
    $started = microtime(true);
    $res = eyvescloud_request($params, $endpoint, $data, $method, $timeout);
    return [
        'response' => $res,
        'debug'    => eyvescloud_debug_entry('EYVESCLOUD API request', [
            'method'   => strtoupper($method),
            'endpoint' => $endpoint,
            'payload'  => $data,
            'http'     => is_array($res) ? ($res['_http_code'] ?? null) : null,
            'success'  => eyvescloud_success($res),
            'message'  => eyvescloud_message($res, ''),
            'ms'       => (int)round((microtime(true) - $started) * 1000),
        ]),
    ];
}

/**
 * 判断面板响应是否成功。
 */
function eyvescloud_success($res)
{
    if (!is_array($res)) {
        return false;
    }
    if (array_key_exists('success', $res)) {
        return (bool)$res['success'];
    }
    if (array_key_exists('status', $res)) {
        $status = $res['status'];
        if ($status === 'success' || $status === true) {
            return true;
        }
        if (is_numeric($status) && (int)$status >= 200 && (int)$status < 300) {
            return true;
        }
        return false;
    }
    if (isset($res['code'])) {
        return (int)$res['code'] >= 200 && (int)$res['code'] < 300;
    }
    return false;
}

/**
 * 提取面板响应中的用户可读消息。
 */
function eyvescloud_message($res, $fallback = '操作失败')
{
    if (!is_array($res)) {
        return (string)$fallback;
    }
    $message = $res['message'] ?? ($res['msg'] ?? ($res['error'] ?? ''));
    if (is_array($message)) {
        $message = reset($message);
    }
    $message = trim((string)$message);
    return $message !== '' ? $message : (string)$fallback;
}

/* -------------------------------------------------------------------------
 * 基础取值 / 格式化工具
 * ---------------------------------------------------------------------- */

function eyvescloud_bool_option($value, $default = false)
{
    if ($value === null || $value === '') {
        return $default;
    }
    if (is_bool($value)) {
        return $value;
    }
    return in_array(strtolower((string)$value), ['1', 'true', 'yes', 'on'], true);
}

function eyvescloud_int_option($options, $key, $default = 0)
{
    if (!isset($options[$key]) || $options[$key] === '') {
        return $default;
    }
    return (int)$options[$key];
}

function eyvescloud_float_option($options, $key, $default = 0)
{
    if (!isset($options[$key]) || $options[$key] === '') {
        return $default;
    }
    return (float)$options[$key];
}

function eyvescloud_number_value($value, $default = 0)
{
    if (is_numeric($value)) {
        return (float)$value;
    }
    if (is_string($value) && preg_match('/-?\d+(?:\.\d+)?/', $value, $match)) {
        return (float)$match[0];
    }
    return $default;
}

function eyvescloud_pick_number($sources, $keys, $default = 0)
{
    foreach ($sources as $source) {
        if (!is_array($source)) {
            continue;
        }
        foreach ($keys as $key) {
            if (array_key_exists($key, $source) && $source[$key] !== '' && $source[$key] !== null) {
                return eyvescloud_number_value($source[$key], $default);
            }
        }
    }
    return $default;
}

function eyvescloud_pct($value)
{
    $value = eyvescloud_number_value($value, 0);
    if ($value < 0) {
        return 0;
    }
    if ($value > 100) {
        return 100;
    }
    return round($value, 2);
}

function eyvescloud_bytes_to_gb($bytes)
{
    return round(eyvescloud_number_value($bytes, 0) / 1073741824, 2);
}

function eyvescloud_bytes_to_mb($bytes)
{
    return round(eyvescloud_number_value($bytes, 0) / 1048576, 2);
}

function eyvescloud_format_bytes($bytes)
{
    $value = eyvescloud_number_value($bytes, 0);
    if ($value >= 1073741824) {
        return round($value / 1073741824, 2) . ' GB';
    }
    if ($value >= 1048576) {
        return round($value / 1048576, 2) . ' MB';
    }
    if ($value >= 1024) {
        return round($value / 1024, 2) . ' KB';
    }
    return round($value, 2) . ' B';
}

function eyvescloud_format_rate($bytesPerSecond)
{
    $value = eyvescloud_number_value($bytesPerSecond, 0);
    if ($value >= 1073741824) {
        return round($value / 1073741824, 2) . ' GB/s';
    }
    if ($value >= 1048576) {
        return round($value / 1048576, 2) . ' MB/s';
    }
    if ($value >= 1024) {
        return round($value / 1024, 2) . ' KB/s';
    }
    return round($value, 2) . ' B/s';
}

function eyvescloud_extra_ports($value)
{
    if (empty($value)) {
        return [];
    }
    $ports = [];
    foreach (preg_split('/[,;\s]+/', (string)$value) as $port) {
        $port = (int)trim($port);
        if ($port > 0 && $port <= 65535) {
            $ports[] = $port;
        }
    }
    return array_values(array_unique($ports));
}

function eyvescloud_csv_values($value)
{
    $parts = is_array($value) ? $value : preg_split('/[,;\s]+/', (string)$value);
    $result = [];
    foreach ($parts as $part) {
        $part = trim((string)$part);
        if ($part !== '') {
            $result[] = $part;
        }
    }
    return array_values(array_unique($result));
}

/**
 * 根据 $params['nextduedate'] 计算同步到面板的到期时间（Y-m-d），未启用同步时返回空串。
 */
function eyvescloud_expiry_from_params($params)
{
    $options = eyvescloud_options($params);
    if (!eyvescloud_bool_option($options['sync_expiry'] ?? 'true', true)) {
        return '';
    }

    $raw = $params['nextduedate'] ?? '';
    if (is_array($raw)) {
        $raw = reset($raw);
    }
    if ($raw === '' || $raw === '0' || $raw === 0 || $raw === '0000-00-00' || $raw === '0000-00-00 00:00:00') {
        return '';
    }

    if (is_numeric($raw)) {
        $timestamp = (int)$raw;
        if ($timestamp > 20000000000) {
            $timestamp = (int)floor($timestamp / 1000);
        }
    } else {
        $timestamp = strtotime((string)$raw);
    }

    if ($timestamp === false || $timestamp <= time()) {
        return '';
    }

    return date('Y-m-d', $timestamp);
}

/* -------------------------------------------------------------------------
 * 实例名 / 公开地址解析
 * ---------------------------------------------------------------------- */

function eyvescloud_container_name($params)
{
    $name = $params['domain'] ?? '';
    if (is_array($name)) {
        $name = reset($name);
    }
    $name = trim((string)$name);
    if ($name === '') {
        $name = 'host-' . ($params['hostid'] ?? ($params['serviceid'] ?? time()));
    }
    $name = preg_replace('/[^A-Za-z0-9_.-]/', '-', $name);
    return trim($name, '-.');
}

function eyvescloud_host_id($params)
{
    foreach (['hostid', 'serviceid', 'service_id', 'id', 'relid'] as $key) {
        if (!empty($params[$key]) && is_numeric($params[$key])) {
            return (int)$params[$key];
        }
    }
    return 0;
}

function eyvescloud_first_string($value)
{
    if (is_array($value)) {
        foreach ($value as $item) {
            if (is_array($item)) {
                foreach (['address', 'ip', 'ipv4', 'public_ip', 'public_ipv4'] as $key) {
                    if (!empty($item[$key])) {
                        $itemValue = trim((string)$item[$key]);
                        if ($itemValue !== '') {
                            return $itemValue;
                        }
                    }
                }
                continue;
            }

            $itemValue = trim((string)$item);
            if ($itemValue !== '') {
                return $itemValue;
            }
        }
        return '';
    }

    return trim((string)$value);
}

function eyvescloud_public_host_from_container($container = [])
{
    if (is_array($container)) {
        foreach (['public_ipv4s', 'public_ipv4', 'public_ip', 'ipv4_addresses', 'ipv4', 'nat_public_ip', 'host_ip', 'external_ip', 'node_ip', 'nat_host'] as $key) {
            if (!empty($container[$key])) {
                $value = eyvescloud_first_string($container[$key]);
                if ($value !== '') {
                    return $value;
                }
            }
        }
    }

    return '';
}

function eyvescloud_public_ipv4_from_routing($params, $container = [])
{
    if (!is_array($container)) {
        $container = [];
    }

    $containerId = isset($container['id']) ? (string)$container['id'] : '';
    $containerName = isset($container['name']) ? (string)$container['name'] : eyvescloud_container_name($params);

    $res = eyvescloud_request($params, '/api/v1/routing', [], 'GET', 30);
    if (!eyvescloud_success($res) || empty($res['data']['ipv4_assignments']) || !is_array($res['data']['ipv4_assignments'])) {
        return '';
    }

    foreach ($res['data']['ipv4_assignments'] as $assignment) {
        if (!is_array($assignment)) {
            continue;
        }
        $matchId = $containerId !== '' && isset($assignment['container_id']) && (string)$assignment['container_id'] === $containerId;
        $matchName = $containerName !== '' && isset($assignment['container_name']) && (string)$assignment['container_name'] === $containerName;
        if ($matchId || $matchName) {
            return eyvescloud_first_string($assignment['address'] ?? '');
        }
    }

    return '';
}

function eyvescloud_public_host($params, $container = [], $useRouting = false)
{
    $fromContainer = eyvescloud_public_host_from_container($container);
    if ($fromContainer !== '') {
        return $fromContainer;
    }

    if ($useRouting) {
        $fromRouting = eyvescloud_public_ipv4_from_routing($params, $container);
        if ($fromRouting !== '') {
            return $fromRouting;
        }
    }

    foreach (['serverip', 'server_ip', 'ip'] as $key) {
        if (!empty($params[$key])) {
            $value = trim((string)$params[$key]);
            if (stripos($value, 'http://') === 0 || stripos($value, 'https://') === 0) {
                return parse_url($value, PHP_URL_HOST) ?: $value;
            }
            return $value;
        }
    }

    return parse_url(eyvescloud_base_url($params), PHP_URL_HOST) ?: '';
}

function eyvescloud_container_ssh_port($container)
{
    if (!is_array($container)) {
        return '';
    }
    foreach (['ssh_port', 'host_ssh_port', 'nat_ssh_port'] as $key) {
        if (isset($container[$key]) && $container[$key] !== '') {
            return (int)$container[$key];
        }
    }
    return '';
}

function eyvescloud_container_password($container)
{
    if (!is_array($container)) {
        return '';
    }
    foreach (['ssh_password', 'password', 'root_password', 'default_password'] as $key) {
        if (isset($container[$key]) && $container[$key] !== '') {
            $password = trim((string)$container[$key]);
            if ($password !== '' && !preg_match('/^\*+$/', $password)) {
                return $password;
            }
        }
    }
    return '';
}

/* -------------------------------------------------------------------------
 * 控制台（WebSSH / VNC）URL
 * ---------------------------------------------------------------------- */

function eyvescloud_console_ws_base($params)
{
    $baseUrl = rtrim(eyvescloud_base_url($params), '/');
    $scheme = stripos($baseUrl, 'https://') === 0 ? 'wss' : 'ws';
    $host = parse_url($baseUrl, PHP_URL_HOST);
    $port = parse_url($baseUrl, PHP_URL_PORT);
    return $scheme . '://' . $host . ($port ? ':' . $port : '');
}

function eyvescloud_webssh_url($params, $ticket, $containerName)
{
    $wsUrl = eyvescloud_console_ws_base($params)
        . '/api/ssh?container=' . rawurlencode((string)$containerName)
        . '&container_name=' . rawurlencode((string)$containerName)
        . '&ticket=' . rawurlencode((string)$ticket);

    $handler = eyvescloud_module_url($params) . 'handlers/webssh.php';

    return $handler
        . '?ws=' . rawurlencode($wsUrl)
        . '&protocol=' . rawurlencode('eyvescloud-ticket.' . (string)$ticket)
        . '&ticket=' . rawurlencode((string)$ticket)
        . '&container=' . rawurlencode((string)$containerName);
}

function eyvescloud_vnc_url($params, $ticket, $containerName)
{
    // VNC WebSocket 代理固定为 /api/vnc（注意：不带 /v1），见 backend/internal/server/server.go。
    // 票据通过 WebSocket 子协议 eyvescloud-vnc-ticket.<ticket> 传递。
    $wsUrl = eyvescloud_console_ws_base($params) . '/api/vnc?container=' . rawurlencode((string)$containerName);

    $handler = eyvescloud_module_url($params) . 'handlers/vnc.php';

    return $handler
        . '?ws=' . rawurlencode($wsUrl)
        . '&protocol=' . rawurlencode('eyvescloud-vnc-ticket.' . (string)$ticket)
        . '&ticket=' . rawurlencode((string)$ticket)
        . '&container=' . rawurlencode((string)$containerName);
}

/* -------------------------------------------------------------------------
 * 请求取值
 * ---------------------------------------------------------------------- */

function eyvescloud_post_value($key, $default = '')
{
    if (isset($_POST[$key])) {
        return $_POST[$key];
    }
    return $default;
}

function eyvescloud_request_value($key, $default = '')
{
    if (isset($_POST[$key])) {
        return $_POST[$key];
    }
    if (isset($_GET[$key])) {
        return $_GET[$key];
    }
    return $default;
}

/**
 * 解析请求体：支持表单编码、JSON、multipart 混合。
 */
function eyvescloud_json_input()
{
    $input = (!empty($_POST) && is_array($_POST)) ? $_POST : [];

    $raw = file_get_contents('php://input');
    if (is_string($raw) && $raw !== '') {
        $data = json_decode($raw, true);
        if (is_array($data)) {
            return array_merge($input, $data);
        }
        $form = [];
        parse_str($raw, $form);
        if (!empty($form) && is_array($form)) {
            return array_merge($input, $form);
        }
    }

    return $input;
}

function eyvescloud_param_value($data, $key, $default = '')
{
    if (is_array($data) && array_key_exists($key, $data)) {
        return $data[$key];
    }
    return eyvescloud_request_value($key, $default);
}

/* -------------------------------------------------------------------------
 * 开通载荷 / 异步等待
 * ---------------------------------------------------------------------- */

/**
 * 由产品配置项构建创建容器载荷（与面板 POST /api/v1/containers 契约一致）。
 */
function eyvescloud_container_payload($params)
{
    $options = eyvescloud_options($params);
    $trafficMode = $options['traffic_mode'] ?? 'total';
    $assignNat = eyvescloud_bool_option($options['assign_nat'] ?? 'true', true);
    $assignIpv4 = eyvescloud_bool_option($options['assign_ipv4'] ?? 'false', false);
    $assignIpv6 = eyvescloud_bool_option($options['assign_ipv6'] ?? 'false', false);
    $publicIpv4s = eyvescloud_csv_values($options['public_ipv4s'] ?? '');
    $ipv6Addresses = eyvescloud_csv_values($options['ipv6_addresses'] ?? '');
    if (!empty($publicIpv4s)) {
        $assignIpv4 = true;
    }
    if (!empty($ipv6Addresses)) {
        $assignIpv6 = true;
    }
    $sshAuthMode = strtolower(trim((string)($options['ssh_auth_mode'] ?? 'auto_password')));
    if (!in_array($sshAuthMode, ['auto_password', 'password', 'key'], true)) {
        $sshAuthMode = 'auto_password';
    }

    return [
        'name'               => eyvescloud_container_name($params),
        'virtualization'     => $options['virtualization'] ?? 'lxc',
        'template_id'        => $options['template_id'] ?? '',
        'vcpu'               => eyvescloud_float_option($options, 'vcpu', 1),
        'cpu_percent'        => eyvescloud_int_option($options, 'cpu_percent', 0),
        'ram_mb'             => eyvescloud_int_option($options, 'ram_mb', 512),
        'disk_gb'            => eyvescloud_float_option($options, 'disk_gb', 5),
        'network_bw_mbps'    => eyvescloud_int_option($options, 'network_bw_mbps', 100),
        'monthly_traffic_gb' => eyvescloud_int_option($options, 'monthly_traffic_gb', 100),
        'traffic_mode'       => in_array($trafficMode, ['total', 'in_out'], true) ? $trafficMode : 'total',
        'traffic_in_gb'      => eyvescloud_int_option($options, 'traffic_in_gb', 0),
        'traffic_out_gb'     => eyvescloud_int_option($options, 'traffic_out_gb', 0),
        'io_speed_mbps'      => eyvescloud_int_option($options, 'io_speed_mbps', 0),
        'extra_ports'        => eyvescloud_extra_ports($options['extra_ports'] ?? ''),
        'port_mapping_count' => $assignNat ? max(2, eyvescloud_int_option($options, 'port_mapping_count', 2)) : 0,
        'assign_nat'         => $assignNat,
        'assign_ipv4'        => $assignIpv4,
        'ipv4_count'         => max(1, eyvescloud_int_option($options, 'ipv4_count', 1)),
        'public_ipv4s'       => $publicIpv4s,
        'snapshot_limit'     => max(1, eyvescloud_int_option($options, 'snapshot_limit', 3)),
        'assign_ipv6'        => $assignIpv6,
        'ipv6_count'         => max(1, eyvescloud_int_option($options, 'ipv6_count', 1)),
        'ipv6_addresses'     => $ipv6Addresses,
        'ssh_auth_mode'      => $sshAuthMode,
        'ssh_password'       => (string)($options['ssh_password'] ?? ''),
        'ssh_public_key'     => trim((string)($options['ssh_public_key'] ?? '')),
        'expires_at'         => eyvescloud_expiry_from_params($params),
    ];
}

function eyvescloud_find_container($params)
{
    $name = eyvescloud_container_name($params);
    return eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name), [], 'GET');
}

function eyvescloud_task_matches_container($task, $name)
{
    if (!is_array($task)) {
        return false;
    }
    $candidates = [
        $task['container_name'] ?? '',
        $task['config']['name'] ?? '',
    ];
    foreach ($candidates as $candidate) {
        $candidate = trim((string)$candidate);
        if ($candidate !== '' && strcasecmp($candidate, (string)$name) === 0) {
            return true;
        }
    }
    return false;
}

/**
 * 有界轮询等待异步开通/重装任务完成。
 *
 * EYVESCLOUD 的开通/重装是异步任务队列，POST 返回时容器可能尚未创建，或
 * ssh_port / ssh_password / status 仍为空。这里轮询任务队列与容器详情，直到容器
 * 就绪或超时。超时不视为失败，而是尽力写回已有字段。
 *
 * @return array{ready:bool,failed:bool,container:array,msg:string}
 */
function eyvescloud_wait_container_ready($params, $timeout = 120)
{
    $interval = 3;
    $name = eyvescloud_container_name($params);
    $deadline = time() + max(0, (int)$timeout);

    // 记录开始前的任务 ID，避免历史失败任务干扰本次判定。
    $baselineTaskIds = [];
    $tasksRes = eyvescloud_request($params, '/api/v1/tasks', [], 'GET', 15);
    if (eyvescloud_success($tasksRes) && !empty($tasksRes['data']) && is_array($tasksRes['data'])) {
        foreach ($tasksRes['data'] as $task) {
            if (is_array($task) && isset($task['id'])) {
                $baselineTaskIds[(string)$task['id']] = true;
            }
        }
    }

    $container = [];
    $lastStatus = '';

    while (true) {
        // 1) 任务队列：本次新增的同名失败任务立即返回错误。
        $tasksRes = eyvescloud_request($params, '/api/v1/tasks', [], 'GET', 15);
        if (eyvescloud_success($tasksRes) && !empty($tasksRes['data']) && is_array($tasksRes['data'])) {
            foreach ($tasksRes['data'] as $task) {
                if (!is_array($task)) {
                    continue;
                }
                $taskId = (string)($task['id'] ?? '');
                if ($taskId !== '' && isset($baselineTaskIds[$taskId])) {
                    continue;
                }
                if (!eyvescloud_task_matches_container($task, $name)) {
                    continue;
                }
                if (strtolower((string)($task['status'] ?? '')) === 'failed') {
                    return [
                        'ready'     => false,
                        'failed'    => true,
                        'container' => $container,
                        'msg'       => '异步任务失败: ' . ($task['error'] ?? '未知错误'),
                    ];
                }
            }
        }

        // 2) 容器详情：状态有效且 SSH 端口/密码至少有一个非空才算就绪。
        $detail = eyvescloud_find_container($params);
        if (eyvescloud_success($detail) && !empty($detail['data']) && is_array($detail['data'])) {
            $container = $detail['data'];
            $lastStatus = strtolower(trim((string)($container['status'] ?? '')));
            $sshPort = eyvescloud_container_ssh_port($container);
            $password = eyvescloud_container_password($container);
            $sshReady = ($sshPort !== '' && (int)$sshPort > 0) || $password !== '';
            if ($lastStatus !== '' && !in_array($lastStatus, ['initializing', 'creating', 'pending'], true) && $sshReady) {
                return [
                    'ready'     => true,
                    'failed'    => false,
                    'container' => $container,
                    'msg'       => '容器已就绪',
                ];
            }
        }

        if (time() >= $deadline) {
            break;
        }
        sleep($interval);
    }

    $msg = '容器仍在初始化，部分字段可能稍后才可用';
    if ($lastStatus !== '') {
        $msg .= '（当前状态: ' . $lastStatus . '）';
    }
    return [
        'ready'     => false,
        'failed'    => false,
        'container' => $container,
        'msg'       => $msg,
    ];
}

/* -------------------------------------------------------------------------
 * 容器 ID / 端口映射
 * ---------------------------------------------------------------------- */

function eyvescloud_container_api_id($params, &$container = null)
{
    $res = eyvescloud_find_container($params);
    if (eyvescloud_success($res) && !empty($res['data']) && is_array($res['data'])) {
        $container = $res['data'];
        if (!empty($container['id'])) {
            return (string)$container['id'];
        }
        if (!empty($container['uuid'])) {
            return (string)$container['uuid'];
        }
        if (!empty($container['name'])) {
            return (string)$container['name'];
        }
    }

    $container = [];
    return eyvescloud_container_name($params);
}

function eyvescloud_port_mappings_from_container($container)
{
    if (!is_array($container)) {
        return [];
    }

    foreach (['port_mappings', 'portMappings', 'nat', 'nat_list', 'NatList'] as $key) {
        if (!empty($container[$key]) && is_array($container[$key])) {
            return $container[$key];
        }
    }

    return [];
}

function eyvescloud_normalize_port_mappings($mappings)
{
    if (!is_array($mappings)) {
        return [];
    }

    $result = [];
    foreach ($mappings as $index => $mapping) {
        if (!is_array($mapping)) {
            continue;
        }
        $protocol = strtolower((string)($mapping['protocol'] ?? 'tcp'));
        $result[] = [
            'index'          => is_numeric($index) ? (int)$index : $index,
            'host_port'      => $mapping['host_port'] ?? '',
            'container_port' => $mapping['container_port'] ?? '',
            'protocol'       => in_array($protocol, ['tcp', 'udp'], true) ? $protocol : 'tcp',
            'description'    => $mapping['description'] ?? '',
        ];
    }

    return $result;
}

function eyvescloud_nat_payload_from_data($data = null)
{
    $hostPort = (int)eyvescloud_param_value($data, 'host_port', 0);
    $containerPort = (int)eyvescloud_param_value($data, 'container_port', 0);
    $protocol = strtolower(trim((string)eyvescloud_param_value($data, 'protocol', 'tcp')));
    $description = trim((string)eyvescloud_param_value($data, 'description', ''));

    if ($hostPort < 1 || $hostPort > 65535) {
        return ['error' => '公网端口必须在 1-65535 之间'];
    }
    if ($containerPort < 1 || $containerPort > 65535) {
        return ['error' => '容器端口必须在 1-65535 之间'];
    }
    if (!in_array($protocol, ['tcp', 'udp'], true)) {
        return ['error' => '协议只支持 tcp 或 udp'];
    }

    return [
        'container_port' => $containerPort,
        'host_port'      => $hostPort,
        'protocol'       => $protocol,
        'description'    => $description,
    ];
}

/* -------------------------------------------------------------------------
 * 面板操作：实例信息
 * ---------------------------------------------------------------------- */

function eyvescloud_normalize_metric_history($points)
{
    if (!is_array($points)) {
        return [];
    }

    $result = [];
    foreach ($points as $point) {
        if (!is_array($point)) {
            continue;
        }
        // 面板 history 返回原始采样 + 小时聚合：ts(毫秒)、cpu/memory(百分比)、
        // network_rx/tx 与 disk_read/write（字节/秒）。统一转成前端图表字段。
        $ts = (int)($point['ts'] ?? 0);
        $result[] = [
            'ts'             => $ts,
            'time'           => $ts > 0 ? date('H:i', (int)floor($ts / 1000)) : '',
            'cpu_percent'    => eyvescloud_pct($point['cpu'] ?? 0),
            'mem_percent'    => eyvescloud_pct($point['memory'] ?? 0),
            'net_in_bps'     => round(eyvescloud_number_value($point['network_rx'] ?? 0, 0), 2),
            'net_out_bps'    => round(eyvescloud_number_value($point['network_tx'] ?? 0, 0), 2),
            'disk_read_bps'  => round(eyvescloud_number_value($point['disk_read'] ?? 0, 0), 2),
            'disk_write_bps' => round(eyvescloud_number_value($point['disk_write'] ?? 0, 0), 2),
        ];
    }

    return $result;
}

function eyvescloud_info_ajax($params)
{
    $debug = [eyvescloud_debug_entry('Info ajax received', ['query' => $_GET])];
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data']) || !is_array($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取实例信息失败'), 'debug' => $debug];
    }

    $c = $res['data'];
    $name = $c['name'] ?? eyvescloud_container_name($params);

    $usageCall = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($name) . '/usage', [], 'GET', 30);
    $trafficCall = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic', [], 'GET', 30);
    // 历史指标：面板提供原始采样 + 小时聚合时序，用于一次性画出历史曲线；
    // 不可用时前端回退到实时值持续采样。
    $historyId = !empty($c['id']) ? (string)$c['id'] : (string)$name;
    $historyCall = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($historyId) . '/history', [], 'GET', 30);
    $debug[] = $usageCall['debug'];
    $debug[] = $trafficCall['debug'];
    $debug[] = $historyCall['debug'];

    $usageRes = $usageCall['response'];
    $usage = eyvescloud_success($usageRes) && isset($usageRes['data']) && is_array($usageRes['data']) ? $usageRes['data'] : [];
    $trafficRes = $trafficCall['response'];
    $traffic = eyvescloud_success($trafficRes) && isset($trafficRes['data']) && is_array($trafficRes['data']) ? $trafficRes['data'] : [];
    $history = eyvescloud_normalize_metric_history(
        eyvescloud_success($historyCall['response']) && isset($historyCall['response']['data']) && is_array($historyCall['response']['data'])
            ? $historyCall['response']['data']
            : []
    );
    $options = eyvescloud_options($params);
    $sources = [$usage, $traffic, $c];

    $rxBytes = eyvescloud_pick_number($sources, ['rx_used_bytes', 'rx_bytes', 'traffic_used_rx', 'in_bytes', 'input_bytes', 'network_rx_bytes'], 0);
    $txBytes = eyvescloud_pick_number($sources, ['tx_used_bytes', 'tx_bytes', 'traffic_used_tx', 'out_bytes', 'output_bytes', 'network_tx_bytes'], 0);
    $totalBytes = eyvescloud_pick_number($sources, ['total_used_bytes', 'traffic_used_bytes'], 0);
    if ($rxBytes <= 0 && $txBytes <= 0 && $totalBytes > 0) {
        $txBytes = $totalBytes;
    }
    if ($rxBytes <= 0) {
        $rxBytes = eyvescloud_pick_number($sources, ['traffic_in_gb', 'in_gb', 'rx_gb'], 0) * 1073741824;
    }
    if ($txBytes <= 0) {
        $txBytes = eyvescloud_pick_number($sources, ['traffic_out_gb', 'out_gb', 'tx_gb'], 0) * 1073741824;
    }

    $limitGB = eyvescloud_pick_number([$traffic, $c, $options], ['monthly_traffic_gb', 'traffic_limit_gb', 'limit_gb'], 0);
    $trafficUsedGB = round(($rxBytes + $txBytes) / 1073741824, 2);
    $trafficPercent = $limitGB > 0 ? eyvescloud_pct(($trafficUsedGB / $limitGB) * 100) : 0;

    $cpuPercent = eyvescloud_pct(eyvescloud_pick_number([$usage, $traffic], ['cpu_usage_pct', 'cpu_percent', 'cpu_usage_percent', 'cpu_usage', 'cpu'], 0));

    $memoryUsedMB = eyvescloud_pick_number($sources, ['memory_used_mb', 'mem_used_mb', 'ram_used_mb', 'memory_usage_mb'], 0);
    if ($memoryUsedMB <= 0) {
        $memoryUsedMB = eyvescloud_bytes_to_mb(eyvescloud_pick_number($sources, ['memory_usage_bytes', 'memory_used', 'mem_used', 'ram_used', 'memory_bytes'], 0));
    }
    $memoryTotalMB = eyvescloud_pick_number([$usage, $c, $options], ['memory_total_mb', 'mem_total_mb', 'ram_total_mb', 'ram_mb'], 0);
    if ($memoryTotalMB <= 0) {
        $memoryTotalMB = eyvescloud_bytes_to_mb(eyvescloud_pick_number($sources, ['memory_total', 'mem_total', 'ram_total'], 0));
    }
    $memoryPercent = $memoryTotalMB > 0 ? eyvescloud_pct(($memoryUsedMB / $memoryTotalMB) * 100) : 0;

    $vcpu = eyvescloud_pick_number([$c, $options], ['vcpu', 'cpu', 'cores'], 1);
    $loadPercent = eyvescloud_pick_number([$usage, $traffic], ['load_percent', 'load_usage_percent'], -1);
    if ($loadPercent < 0) {
        $loadValue = eyvescloud_pick_number([$usage, $traffic], ['load1', 'load', 'load_average'], 0);
        $loadPercent = $vcpu > 0 ? ($loadValue / $vcpu) * 100 : 0;
    }
    $loadPercent = eyvescloud_pct($loadPercent);

    $diskUsedGB = eyvescloud_pick_number($sources, ['disk_used_gb', 'disk_usage_gb', 'storage_used_gb'], 0);
    if ($diskUsedGB <= 0) {
        $diskUsedGB = eyvescloud_bytes_to_gb(eyvescloud_pick_number($sources, ['disk_usage_bytes', 'disk_used', 'disk_usage', 'storage_used'], 0));
    }
    $diskTotalGB = eyvescloud_pick_number([$usage, $c, $options], ['disk_total_gb', 'storage_total_gb', 'disk_gb'], 0);
    if ($diskTotalGB <= 0) {
        $diskTotalGB = eyvescloud_bytes_to_gb(eyvescloud_pick_number($sources, ['disk_total', 'storage_total'], 0));
    }
    $diskPercent = $diskTotalGB > 0 ? eyvescloud_pct(($diskUsedGB / $diskTotalGB) * 100) : 0;

    $netInBps = eyvescloud_pick_number($sources, ['rx_bps', 'in_bps', 'network_rx_bps', 'net_in_bps'], 0);
    $netOutBps = eyvescloud_pick_number($sources, ['tx_bps', 'out_bps', 'network_tx_bps', 'net_out_bps'], 0);
    $diskReadBps = eyvescloud_pick_number($sources, ['disk_read_bps', 'read_bps', 'io_read_bps'], 0);
    $diskWriteBps = eyvescloud_pick_number($sources, ['disk_write_bps', 'write_bps', 'io_write_bps'], 0);

    $statusRaw = strtolower((string)($c['status'] ?? ''));
    $statusText = $statusRaw === 'running' ? '运行中' : (in_array($statusRaw, ['stopped', 'shutdown'], true) ? '已关机' : ($statusRaw !== '' ? $statusRaw : '未知'));

    return [
        'status' => 'success',
        'msg'    => '获取成功',
        'data'   => [
            'name'           => $name,
            'status'         => $statusRaw,
            'status_text'    => $statusText,
            'server_ip'      => eyvescloud_public_host($params, $c, true),
            'ssh_port'       => $c['ssh_port'] ?? '',
            'ipv4'           => $c['ip'] ?? '',
            'ipv6'           => $c['ipv6'] ?? '',
            'vcpu'           => $c['vcpu'] ?? ($options['vcpu'] ?? ''),
            'ram_mb'         => $c['ram_mb'] ?? ($options['ram_mb'] ?? ''),
            'disk_gb'        => $c['disk_gb'] ?? ($options['disk_gb'] ?? ''),
            'bandwidth'      => $c['network_bw_mbps'] ?? ($options['network_bw_mbps'] ?? ''),
            'expires_at'     => $c['expires_at'] ?? '',
            'cpu_percent'    => $cpuPercent,
            'cpu_detail'     => $cpuPercent . '%',
            'mem_percent'    => $memoryPercent,
            'mem_detail'     => ($memoryTotalMB > 0 ? round($memoryUsedMB, 0) . ' / ' . round($memoryTotalMB, 0) . ' MB' : '-'),
            'load_percent'   => $loadPercent,
            'load_detail'    => $loadPercent . '%',
            'disk_percent'   => $diskPercent,
            'disk_detail'    => ($diskTotalGB > 0 ? round($diskUsedGB, 2) . ' / ' . round($diskTotalGB, 2) . ' GB' : '-'),
            'traffic_used'   => $trafficUsedGB,
            'traffic_limit'  => $limitGB,
            'traffic_in_gb'  => round($rxBytes / 1073741824, 2),
            'traffic_out_gb' => round($txBytes / 1073741824, 2),
            'traffic_used_text'  => eyvescloud_format_bytes($rxBytes + $txBytes),
            'traffic_limit_text' => $limitGB > 0 ? round($limitGB, 2) . ' GB' : '不限',
            'traffic_in_text'    => eyvescloud_format_bytes($rxBytes),
            'traffic_out_text'   => eyvescloud_format_bytes($txBytes),
            'traffic_percent'    => $trafficPercent,
            'net_in_bps'     => round($netInBps, 2),
            'net_out_bps'    => round($netOutBps, 2),
            'net_in_rate'    => eyvescloud_format_rate($netInBps),
            'net_out_rate'   => eyvescloud_format_rate($netOutBps),
            'disk_read_bps'  => round($diskReadBps, 2),
            'disk_write_bps' => round($diskWriteBps, 2),
            'disk_read_rate' => eyvescloud_format_rate($diskReadBps),
            'disk_write_rate'=> eyvescloud_format_rate($diskWriteBps),
            'chart_time'     => date('H:i:s'),
            'history'        => $history,
        ],
        'debug'  => $debug,
    ];
}

function eyvescloud_infoData($params)
{
    $data = eyvescloud_info_ajax($params);
    if (($data['status'] ?? '') !== 'success') {
        return [
            'status' => 'error',
            'msg'    => $data['msg'] ?? '流量统计暂不可用',
            'data'   => [
                'cpu_percent'    => 0,
                'cpu_detail'     => '-',
                'mem_percent'    => 0,
                'mem_detail'     => '-',
                'load_percent'   => 0,
                'load_detail'    => '-',
                'disk_percent'   => 0,
                'disk_detail'    => '-',
                'traffic_used'   => '-',
                'traffic_limit'  => '-',
                'traffic_in_gb'  => '-',
                'traffic_out_gb' => '-',
                'traffic_used_text' => '-',
                'traffic_limit_text'=> '-',
                'traffic_in_text'   => '-',
                'traffic_out_text'  => '-',
                'traffic_percent'=> 0,
                'net_in_bps'     => 0,
                'net_out_bps'    => 0,
                'net_in_rate'    => '0 B/s',
                'net_out_rate'   => '0 B/s',
                'disk_read_bps'  => 0,
                'disk_write_bps' => 0,
                'disk_read_rate' => '0 B/s',
                'disk_write_rate'=> '0 B/s',
                'chart_time'     => date('H:i:s'),
                'history'        => [],
            ],
            'debug' => $data['debug'] ?? [],
        ];
    }

    $data['data']['debug'] = $data['debug'] ?? [];
    return ['status' => 'success', 'msg' => '获取成功', 'data' => $data['data']];
}

/* -------------------------------------------------------------------------
 * 面板操作：NAT 端口映射
 * ---------------------------------------------------------------------- */

function eyvescloud_nat_ajax($params)
{
    $input = eyvescloud_json_input();
    $action = strtolower(trim((string)eyvescloud_param_value($input, 'action', '')));
    $debug = [eyvescloud_debug_entry('NAT ajax received', ['action' => $action, 'input' => $input, 'query' => $_GET])];

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $debug[] = eyvescloud_debug_entry('Container resolved', [
        'container_id' => $containerId,
        'container'    => [
            'id'   => $container['id'] ?? null,
            'uuid' => $container['uuid'] ?? null,
            'name' => $container['name'] ?? null,
        ],
    ]);

    if (!in_array($action, ['random-port', 'add', 'update', 'delete'], true)) {
        return ['status' => 'error', 'msg' => '未知 NAT 操作', 'debug' => $debug];
    }

    if ($action === 'random-port') {
        $call = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($containerId) . '/random-port', [], 'GET', 30);
        $debug[] = $call['debug'];
        $res = $call['response'];
        return eyvescloud_success($res)
            ? ['status' => 'success', 'msg' => '随机端口: ' . ($res['data']['port'] ?? ''), 'data' => ['port' => $res['data']['port'] ?? ''], 'debug' => $debug]
            : ['status' => 'error', 'msg' => eyvescloud_message($res, '获取随机端口失败'), 'debug' => $debug];
    }

    if ($action === 'delete') {
        $index = eyvescloud_param_value($input, 'index', '');
        if ($index === '' || !is_numeric($index) || (int)$index < 0) {
            return ['status' => 'error', 'msg' => '端口映射索引错误', 'debug' => $debug];
        }
        $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings/' . rawurlencode((string)(int)$index);
        $call = eyvescloud_request_debug($params, $endpoint, [], 'DELETE', 30);
        $debug[] = $call['debug'];
        $res = $call['response'];
    } else {
        $payload = eyvescloud_nat_payload_from_data($input);
        if (isset($payload['error'])) {
            return ['status' => 'error', 'msg' => $payload['error'], 'debug' => $debug];
        }

        if ($action === 'add') {
            $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings';
            $call = eyvescloud_request_debug($params, $endpoint, $payload, 'POST', 30);
        } else {
            $index = eyvescloud_param_value($input, 'index', '');
            if ($index === '' || !is_numeric($index) || (int)$index < 0) {
                return ['status' => 'error', 'msg' => '端口映射索引错误', 'debug' => $debug];
            }
            $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings/' . rawurlencode((string)(int)$index);
            $call = eyvescloud_request_debug($params, $endpoint, $payload, 'PUT', 30);
        }
        $debug[] = $call['debug'];
        $res = $call['response'];
    }

    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, 'NAT 操作失败'), 'debug' => $debug];
    }

    return [
        'status' => 'success',
        'msg'    => eyvescloud_message($res, 'NAT 操作成功'),
        'data'   => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])],
        'debug'  => $debug,
    ];
}

function eyvescloud_randomPort($params)
{
    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/random-port', [], 'GET', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取随机端口失败')];
    }

    $port = $res['data']['port'] ?? '';
    return ['status' => 'success', 'msg' => $port ? '随机端口: ' . $port : '随机端口获取成功', 'data' => ['port' => $port]];
}

function eyvescloud_addNat($params)
{
    $payload = eyvescloud_nat_payload_from_data(null);
    if (isset($payload['error'])) {
        return ['status' => 'error', 'msg' => $payload['error']];
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings', $payload, 'POST', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '端口映射添加成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '端口映射添加失败')];
}

function eyvescloud_updateNat($params)
{
    $index = eyvescloud_request_value('index', '');
    if ($index === '' || !is_numeric($index) || (int)$index < 0) {
        return ['status' => 'error', 'msg' => '端口映射索引错误'];
    }

    $payload = eyvescloud_nat_payload_from_data(null);
    if (isset($payload['error'])) {
        return ['status' => 'error', 'msg' => $payload['error']];
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings/' . rawurlencode((string)(int)$index);
    $res = eyvescloud_request($params, $endpoint, $payload, 'PUT', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '端口映射更新成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '端口映射更新失败')];
}

function eyvescloud_deleteNat($params)
{
    $index = eyvescloud_request_value('index', '');
    if ($index === '' || !is_numeric($index) || (int)$index < 0) {
        return ['status' => 'error', 'msg' => '端口映射索引错误'];
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings/' . rawurlencode((string)(int)$index);
    $res = eyvescloud_request($params, $endpoint, [], 'DELETE', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '端口映射删除成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '端口映射删除失败')];
}

function eyvescloud_natList($params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data']) || !is_array($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取 NAT 列表失败')];
    }

    return [
        'status' => 'success',
        'msg'    => '获取成功',
        'data'   => [
            'container_name' => $res['data']['name'] ?? eyvescloud_container_name($params),
            'server_ip'      => eyvescloud_public_host($params, $res['data'], true),
            'ssh_port'       => $res['data']['ssh_port'] ?? '',
            'port_mappings'  => eyvescloud_normalize_port_mappings(eyvescloud_port_mappings_from_container($res['data'])),
        ],
    ];
}

/* -------------------------------------------------------------------------
 * 面板操作：防火墙
 * ---------------------------------------------------------------------- */

function eyvescloud_firewallList($params)
{
    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', [], 'GET', 30);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取防火墙设置失败')];
    }

    return ['status' => 'success', 'msg' => '获取成功', 'data' => $res['data']];
}

function eyvescloud_firewallUpdate($params)
{
    $input = eyvescloud_json_input();
    $enabled = eyvescloud_param_value($input, 'enabled', 'true');
    $enabled = filter_var($enabled, FILTER_VALIDATE_BOOLEAN);
    $defaultAction = strtoupper(trim((string)eyvescloud_param_value($input, 'default_action', '')));
    $rules = eyvescloud_param_value($input, 'rules', '[]');

    if (is_string($rules)) {
        $decodedRules = json_decode($rules, true);
        if (is_array($decodedRules)) {
            $rules = $decodedRules;
        }
    }
    if (!is_array($rules)) {
        $rules = [];
    }

    $payload = [
        'enabled' => $enabled,
        'rules'   => $rules,
    ];
    if (in_array($defaultAction, ['ACCEPT', 'DROP'], true)) {
        $payload['default_action'] = $defaultAction;
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', $payload, 'PUT', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '更新防火墙设置失败')];
    }

    // PUT 后回读，确认面板处理后的实际状态。
    $getRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', [], 'GET', 30);
    $actualData = [];
    if (eyvescloud_success($getRes) && !empty($getRes['data']) && is_array($getRes['data'])) {
        $actualData = $getRes['data'];
    }

    return ['status' => 'success', 'msg' => eyvescloud_message($res, '防火墙设置已更新'), 'data' => $actualData];
}

function eyvescloud_firewall_ajax($params)
{
    $input = eyvescloud_json_input();
    $action = strtolower(trim((string)eyvescloud_param_value($input, 'action', '')));

    if ($action === 'list') {
        return eyvescloud_firewallList($params);
    }
    if ($action === 'update') {
        return eyvescloud_firewallUpdate($params);
    }
    return ['status' => 'error', 'msg' => '未知防火墙操作'];
}

/* -------------------------------------------------------------------------
 * 面板操作：快照 / 备份 / ISO
 * ---------------------------------------------------------------------- */

function eyvescloud_normalize_assets($items, $sizeKey = 'size_bytes')
{
    $out = [];
    foreach ((array)$items as $item) {
        if (!is_array($item)) {
            continue;
        }
        $item['size_text'] = eyvescloud_format_bytes((int)($item[$sizeKey] ?? 0));
        $out[] = $item;
    }
    return $out;
}

function eyvescloud_snapshot_base($params)
{
    return '/api/v1/containers/' . rawurlencode(eyvescloud_container_name($params)) . '/snapshots';
}

function eyvescloud_snapshotList($params)
{
    $res = eyvescloud_request($params, eyvescloud_snapshot_base($params), [], 'GET', 30);
    if (!eyvescloud_success($res) || !isset($res['data']['snapshots'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取快照列表失败')];
    }
    return ['status' => 'success', 'msg' => '获取成功', 'data' => [
        'snapshots' => eyvescloud_normalize_assets($res['data']['snapshots']),
        'quota'     => $res['data']['quota'] ?? 0,
    ]];
}

function eyvescloud_snapshotCreate($params)
{
    $res = eyvescloud_request($params, eyvescloud_snapshot_base($params), [], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '创建快照失败')];
    }
    return ['status' => 'success', 'msg' => '快照已创建', 'data' => []];
}

function eyvescloud_snapshotID()
{
    return trim((string)eyvescloud_request_value('id', ''));
}

function eyvescloud_snapshotRestore($params)
{
    $sid = eyvescloud_snapshotID();
    if ($sid === '') {
        return ['status' => 'error', 'msg' => '缺少快照 ID'];
    }
    $res = eyvescloud_request($params, eyvescloud_snapshot_base($params) . '/' . rawurlencode($sid) . '/restore', [], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '还原快照失败')];
    }
    return ['status' => 'success', 'msg' => '还原任务已提交', 'data' => []];
}

function eyvescloud_snapshotDelete($params)
{
    $sid = eyvescloud_snapshotID();
    if ($sid === '') {
        return ['status' => 'error', 'msg' => '缺少快照 ID'];
    }
    $res = eyvescloud_request($params, eyvescloud_snapshot_base($params) . '/' . rawurlencode($sid), [], 'DELETE', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '删除快照失败')];
    }
    return ['status' => 'success', 'msg' => '快照已删除', 'data' => []];
}

function eyvescloud_backup_base($params)
{
    return '/api/v1/containers/' . rawurlencode(eyvescloud_container_name($params)) . '/backups';
}

function eyvescloud_backupList($params)
{
    $res = eyvescloud_request($params, eyvescloud_backup_base($params), [], 'GET', 30);
    if (!eyvescloud_success($res) || !isset($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取备份列表失败')];
    }
    return ['status' => 'success', 'msg' => '获取成功', 'data' => ['backups' => eyvescloud_normalize_assets($res['data'])]];
}

function eyvescloud_backupCreate($params)
{
    $res = eyvescloud_request($params, eyvescloud_backup_base($params), [], 'POST', 180);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '创建备份失败')];
    }
    return ['status' => 'success', 'msg' => '备份已创建', 'data' => []];
}

function eyvescloud_backupRestore($params)
{
    $bid = trim((string)eyvescloud_request_value('id', ''));
    if ($bid === '') {
        return ['status' => 'error', 'msg' => '缺少备份 ID'];
    }
    $res = eyvescloud_request($params, eyvescloud_backup_base($params) . '/' . rawurlencode($bid) . '/restore', [], 'POST', 180);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '还原备份失败')];
    }
    return ['status' => 'success', 'msg' => '还原任务已提交', 'data' => []];
}

function eyvescloud_backupDelete($params)
{
    $bid = trim((string)eyvescloud_request_value('id', ''));
    if ($bid === '') {
        return ['status' => 'error', 'msg' => '缺少备份 ID'];
    }
    $res = eyvescloud_request($params, eyvescloud_backup_base($params) . '/' . rawurlencode($bid), [], 'DELETE', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '删除备份失败')];
    }
    return ['status' => 'success', 'msg' => '备份已删除', 'data' => []];
}

function eyvescloud_isoList($params)
{
    $res = eyvescloud_request($params, '/api/isos', [], 'GET', 30);
    if (!eyvescloud_success($res) || !isset($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取 ISO 列表失败')];
    }
    return ['status' => 'success', 'msg' => '获取成功', 'data' => [
        'isos'         => eyvescloud_normalize_assets($res['data']),
        'container_id' => eyvescloud_isoNumericID($params),
    ]];
}

function eyvescloud_isoNumericID($params)
{
    $container = [];
    $id = (int)eyvescloud_container_api_id($params, $container);
    if ($id <= 0 && !empty($container['id'])) {
        $id = (int)$container['id'];
    }
    return $id;
}

function eyvescloud_isoAttach($params)
{
    $isoId = trim((string)eyvescloud_request_value('iso_id', ''));
    if ($isoId === '') {
        return ['status' => 'error', 'msg' => '缺少 ISO ID'];
    }
    $cid = eyvescloud_isoNumericID($params);
    if ($cid <= 0) {
        return ['status' => 'error', 'msg' => '无法解析容器编号，请确认实例名称与面板一致'];
    }
    $res = eyvescloud_request($params, '/api/isos/attach', ['container_id' => $cid, 'iso_id' => $isoId, 'attach' => true], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '挂载 ISO 失败')];
    }
    return ['status' => 'success', 'msg' => 'ISO 已挂载', 'data' => []];
}

function eyvescloud_isoDetach($params)
{
    $cid = eyvescloud_isoNumericID($params);
    if ($cid <= 0) {
        return ['status' => 'error', 'msg' => '无法解析容器编号，请确认实例名称与面板一致'];
    }
    $res = eyvescloud_request($params, '/api/isos/attach', ['container_id' => $cid, 'iso_id' => '', 'attach' => false], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '卸载 ISO 失败')];
    }
    return ['status' => 'success', 'msg' => 'ISO 已卸载', 'data' => []];
}

/* -------------------------------------------------------------------------
 * 面板操作：重装系统
 * ---------------------------------------------------------------------- */

function eyvescloud_reinstallTemplates($params)
{
    $res = eyvescloud_request($params, '/api/v1/templates', [], 'GET', 30);
    if (!eyvescloud_success($res) || !isset($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取系统模板失败')];
    }
    $list = [];
    foreach ((array)$res['data'] as $t) {
        if (!is_array($t)) {
            continue;
        }
        $list[] = [
            'id'   => $t['id'] ?? '',
            'name' => $t['name'] ?? ($t['id'] ?? ''),
            'arch' => $t['arch'] ?? '',
        ];
    }
    $container = [];
    eyvescloud_container_api_id($params, $container);
    return ['status' => 'success', 'msg' => '获取成功', 'data' => [
        'templates'       => $list,
        'current_template'=> $container['template'] ?? (eyvescloud_options($params)['template_id'] ?? ''),
    ]];
}

function eyvescloud_client_reinstall($params)
{
    $templateId = trim((string)eyvescloud_request_value('template_id', ''));
    if ($templateId === '') {
        return ['status' => 'error', 'msg' => '请选择要重装的系统模板'];
    }
    $reinstallMode = trim((string)eyvescloud_request_value('reinstall_mode', ''));
    $payload = ['template_id' => $templateId];
    if ($reinstallMode !== '') {
        $payload['reinstall_mode'] = $reinstallMode;
    }
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode(eyvescloud_container_name($params)) . '/reinstall', $payload, 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '重装失败')];
    }
    return ['status' => 'success', 'msg' => '重装任务已提交', 'data' => []];
}

/* -------------------------------------------------------------------------
 * 面板操作：电源 / 删除 / 密码 / 资源 / 流量 / 到期 / 用量 / 控制台票据
 * ---------------------------------------------------------------------- */

function eyvescloud_container_action($params, $action, $successMsg, $timeout = 60)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/' . $action, [], 'POST', $timeout);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, $successMsg), 'data' => []]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, $successMsg . '失败')];
}

function eyvescloud_container_delete($params)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/delete', [], 'DELETE', 60);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '删除任务已提交'), 'data' => []]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '删除失败')];
}

function eyvescloud_reset_password($params, $newPassword)
{
    $newPassword = trim((string)$newPassword);
    if ($newPassword === '') {
        return ['status' => 'error', 'msg' => '新密码不能为空'];
    }

    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/reset-password', ['password' => $newPassword], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '重置密码失败')];
    }

    $password = $res['data']['ssh_password'] ?? ($res['data']['password'] ?? $newPassword);
    $hostId = eyvescloud_host_id($params);
    if ($hostId > 0 && class_exists('\WHMCS\Database\Capsule')) {
        try {
            \WHMCS\Database\Capsule::table('tblhosting')->where('id', $hostId)->update([
                'password' => eyvescloud_store_password($password),
            ]);
            $detail = eyvescloud_find_container($params);
            if (eyvescloud_success($detail) && isset($detail['data'])) {
                eyvescloud_update_host_from_container($params, $detail['data']);
            }
        } catch (\Throwable $e) {
            return ['status' => 'error', 'msg' => '密码重置成功但同步计费系统失败: ' . $e->getMessage()];
        }
    }

    return ['status' => 'success', 'msg' => eyvescloud_message($res, '密码重置成功'), 'data' => ['password' => $password]];
}

function eyvescloud_resource_limit($params)
{
    $options = eyvescloud_options($params);
    $name = eyvescloud_container_name($params);

    $resource = [
        'vcpu'            => eyvescloud_float_option($options, 'vcpu', 0),
        'ram_mb'          => eyvescloud_int_option($options, 'ram_mb', 0),
        'disk_gb'         => eyvescloud_float_option($options, 'disk_gb', 0),
        'io_speed_mbps'   => eyvescloud_int_option($options, 'io_speed_mbps', 0),
        'network_bw_mbps' => eyvescloud_int_option($options, 'network_bw_mbps', 0),
    ];
    $resource = array_filter($resource, function ($value) {
        return $value !== 0 && $value !== 0.0;
    });

    if (empty($resource)) {
        return ['status' => 'success', 'msg' => '没有需要调整的资源限制', 'data' => []];
    }

    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/resource-limit', $resource, 'PUT', 60);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '资源限制调整成功'), 'data' => []]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '资源限制调整失败')];
}

function eyvescloud_traffic_limit($params)
{
    $options = eyvescloud_options($params);
    $name = eyvescloud_container_name($params);

    $traffic = [
        'traffic_mode'       => $options['traffic_mode'] ?? 'total',
        'monthly_traffic_gb' => eyvescloud_int_option($options, 'monthly_traffic_gb', 0),
        'traffic_in_gb'      => eyvescloud_int_option($options, 'traffic_in_gb', 0),
        'traffic_out_gb'     => eyvescloud_int_option($options, 'traffic_out_gb', 0),
    ];
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic-limit', $traffic, 'PUT', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '流量限制调整成功'), 'data' => []]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '流量限制调整失败')];
}

function eyvescloud_set_expiry($params)
{
    $expiresAt = eyvescloud_expiry_from_params($params);
    if ($expiresAt === '') {
        return ['status' => 'success', 'msg' => '未启用到期时间同步', 'data' => []];
    }
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/expiry', ['expires_at' => $expiresAt], 'PUT', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => '到期时间同步成功', 'data' => ['expires_at' => $expiresAt]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '到期时间同步失败')];
}

function eyvescloud_traffic_reset($params)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic-reset', [], 'POST', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '流量已重置'), 'data' => []]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '流量重置失败')];
}

/**
 * 读取单个实例的用量（usage + traffic），返回规整后的字节/限额数据。
 *
 * @return array{ok:bool,msg:string,rx_bytes:float,tx_bytes:float,total_bytes:float,limit_gb:float,raw_usage:array,raw_traffic:array}
 */
function eyvescloud_container_usage($params, $name = '')
{
    if ($name === '') {
        $name = eyvescloud_container_name($params);
    }

    $usageRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/usage', [], 'GET', 30);
    $trafficRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic', [], 'GET', 30);

    $usage = eyvescloud_success($usageRes) && isset($usageRes['data']) && is_array($usageRes['data']) ? $usageRes['data'] : [];
    $traffic = eyvescloud_success($trafficRes) && isset($trafficRes['data']) && is_array($trafficRes['data']) ? $trafficRes['data'] : [];

    if (empty($usage) && empty($traffic)) {
        return [
            'ok'    => false,
            'msg'   => eyvescloud_message($trafficRes, eyvescloud_message($usageRes, '获取用量失败')),
            'rx_bytes' => 0, 'tx_bytes' => 0, 'total_bytes' => 0, 'limit_gb' => 0,
            'raw_usage' => $usage, 'raw_traffic' => $traffic,
        ];
    }

    $rxBytes = eyvescloud_pick_number([$traffic, $usage], ['rx_used_bytes', 'traffic_used_rx', 'rx_bytes', 'network_rx_bytes'], 0);
    $txBytes = eyvescloud_pick_number([$traffic, $usage], ['tx_used_bytes', 'traffic_used_tx', 'tx_bytes', 'network_tx_bytes'], 0);
    $totalBytes = eyvescloud_pick_number([$traffic], ['total_used_bytes'], 0);
    if ($totalBytes <= 0) {
        $totalBytes = $rxBytes + $txBytes;
    }
    $limitGB = eyvescloud_pick_number([$traffic], ['limit_gb', 'monthly_traffic_gb'], 0);

    return [
        'ok'          => true,
        'msg'         => '用量获取成功',
        'rx_bytes'    => $rxBytes,
        'tx_bytes'    => $txBytes,
        'total_bytes' => $totalBytes,
        'limit_gb'    => $limitGB,
        'raw_usage'   => $usage,
        'raw_traffic' => $traffic,
    ];
}

function eyvescloud_ssh_ticket($params)
{
    $container = [];
    $containerName = eyvescloud_container_name($params);
    eyvescloud_container_api_id($params, $container);
    if (!empty($container['name'])) {
        $containerName = (string)$container['name'];
    }

    $res = eyvescloud_request($params, '/api/v1/ssh-ticket', ['container_name' => $containerName], 'POST', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, 'WebSSH 票据创建失败')];
    }

    $ticket = $res['data']['ticket'] ?? '';
    if ($ticket === '') {
        return ['status' => 'error', 'msg' => 'WebSSH 票据为空'];
    }

    return ['status' => 'success', 'msg' => '控制台已就绪', 'data' => [
        'url'      => eyvescloud_webssh_url($params, $ticket, $containerName),
        'container'=> $containerName,
    ]];
}

function eyvescloud_vnc_ticket($params)
{
    $container = [];
    $containerName = eyvescloud_container_name($params);
    eyvescloud_container_api_id($params, $container);
    if (!empty($container['name'])) {
        $containerName = (string)$container['name'];
    }

    // KVM VNC 票据：POST /api/v1/vnc-ticket，请求体 {"container_name": "..."}，返回 {"data": {"ticket": "..."}}。
    $res = eyvescloud_request($params, '/api/v1/vnc-ticket', ['container_name' => $containerName], 'POST', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, 'VNC 票据创建失败')];
    }

    $ticket = $res['data']['ticket'] ?? '';
    if ($ticket === '') {
        return ['status' => 'error', 'msg' => 'VNC 票据为空'];
    }

    return ['status' => 'success', 'msg' => 'VNC 控制台已就绪', 'data' => [
        'url'      => eyvescloud_vnc_url($params, $ticket, $containerName),
        'container'=> $containerName,
    ]];
}

/* -------------------------------------------------------------------------
 * 状态 / 写回 WHMCS
 * ---------------------------------------------------------------------- */

function eyvescloud_domain_status_from_container($container)
{
    if (!is_array($container)) {
        return 'Active';
    }

    if (!empty($container['policy_blocked'])) {
        return 'Suspended';
    }

    $status = strtolower(trim((string)($container['status'] ?? '')));
    if (in_array($status, ['suspended', 'blocked', 'policy_blocked', 'disabled'], true)) {
        return 'Suspended';
    }

    return 'Active';
}

function eyvescloud_container_status($params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '查询失败')];
    }

    $raw = strtolower((string)($res['data']['status'] ?? ''));
    if ($raw === 'running') {
        return ['status' => 'success', 'msg' => '运行中', 'data' => ['state' => 'on', 'status_text' => '运行中', 'container' => $res['data']]];
    }
    if (in_array($raw, ['stopped', 'shutdown'], true)) {
        return ['status' => 'success', 'msg' => '已关机', 'data' => ['state' => 'off', 'status_text' => '已关机', 'container' => $res['data']]];
    }
    return ['status' => 'success', 'msg' => $raw ?: '未知', 'data' => ['state' => 'unknown', 'status_text' => $raw ?: '未知', 'container' => $res['data']]];
}

/**
 * 把面板容器状态写回 WHMCS 主机表（dedicatedip / username / password / port / domainstatus）。
 */
function eyvescloud_update_host_from_container($params, $container)
{
    $hostId = eyvescloud_host_id($params);
    if ($hostId <= 0 || !is_array($container) || !class_exists('\WHMCS\Database\Capsule')) {
        return;
    }

    // WHMCS 主机表无端口字段，因此只写回 dedicatedip / username / password / domainstatus。
    $update = [
        'domainstatus' => eyvescloud_domain_status_from_container($container),
        'username'     => 'root',
        'dedicatedip'  => eyvescloud_public_host($params, $container, true),
    ];

    $password = eyvescloud_container_password($container);
    if ($password !== '') {
        $update['password'] = eyvescloud_store_password($password);
    }

    try {
        \WHMCS\Database\Capsule::table('tblhosting')->where('id', $hostId)->update($update);
    } catch (\Throwable $e) {
        eyvescloud_debug('tblhosting update failed', $e->getMessage());
    }
}

/* -------------------------------------------------------------------------
 * WHMCS 数据库 -> $params 适配（供 handlers/api.php 使用）
 * ---------------------------------------------------------------------- */

/**
 * 从 WHMCS 数据库重建模块标准 $params（等价于 WHMCS 调用模块时传入的结构）。
 *
 * 仅在独立入口 handlers/api.php 中使用；模块被 WHMCS 调用时无需此函数。
 * serveraccesshash / serverpassword 会在写入时加密，这里统一解密。
 *
 * @param int $serviceid
 * @return array
 */
function eyvescloud_service_params($serviceid)
{
    if (!class_exists('\WHMCS\Database\Capsule')) {
        return [];
    }

    $serviceid = (int)$serviceid;
    if ($serviceid <= 0) {
        return [];
    }

    $host = \WHMCS\Database\Capsule::table('tblhosting')->where('id', $serviceid)->first();
    if (!$host) {
        return [];
    }
    $host = (array)$host;

    $server = [];
    if (!empty($host['server'])) {
        $row = \WHMCS\Database\Capsule::table('tblservers')->where('id', $host['server'])->first();
        if ($row) {
            $server = (array)$row;
        }
    }

    $product = [];
    if (!empty($host['packageid'])) {
        $row = \WHMCS\Database\Capsule::table('tblproducts')->where('id', $host['packageid'])->first();
        if ($row) {
            $product = (array)$row;
        }
    }

    $params = [
        'serviceid'         => (int)$host['id'],
        'hostid'            => (int)$host['id'],
        'userid'            => $host['userid'] ?? 0,
        'domain'            => $host['domain'] ?? '',
        'username'          => $host['username'] ?? '',
        'password'          => eyvescloud_decrypt($host['password'] ?? ''),
        'nextduedate'       => $host['nextduedate'] ?? '',
        'billingcycle'      => $host['billingcycle'] ?? '',
        'domainstatus'      => $host['domainstatus'] ?? '',
        'packageid'         => $host['packageid'] ?? 0,
        'serverid'          => $host['server'] ?? 0,
        'customfields'      => [],
        'configoptions'     => [],
        'serverhostname'    => $server['hostname'] ?? '',
        'serverip'          => $server['ipaddress'] ?? '',
        'serverport'        => $server['port'] ?? '',
        'serversecure'      => $server['secure'] ?? '',
        'serverusername'    => $server['username'] ?? '',
        'serverpassword'    => eyvescloud_decrypt($server['password'] ?? ''),
        'serveraccesshash'  => eyvescloud_decrypt($server['accesshash'] ?? ''),
    ];

    for ($i = 1; $i <= 24; $i++) {
        $params['configoption' . $i] = $product['configoption' . $i] ?? '';
    }

    // 按中文标签合并一份 configoptions，便于 eyvescloud_options 双重兜底。
    $labelMap = eyvescloud_option_labels();
    foreach ($labelMap as $label => $key) {
        if (isset($params['configoption' . (array_search($key, eyvescloud_option_keys(), true) + 1)])) {
            $params['configoptions'][$label] = $params['configoption' . (array_search($key, eyvescloud_option_keys(), true) + 1)];
        }
    }

    // 客户详情（部分场景需要，例如 SSO 显示）。
    if (!empty($host['userid'])) {
        $client = \WHMCS\Database\Capsule::table('tblclients')->where('id', $host['userid'])->first();
        if ($client) {
            $params['clientsdetails'] = (array)$client;
        }
    }

    return $params;
}

/* -------------------------------------------------------------------------
 * 客户区 AJAX 统一分发器
 * ---------------------------------------------------------------------- */

/**
 * 将面板操作结果规整为统一 JSON 结构：{success, message, data, debug}。
 *
 * @param mixed $res
 * @return array
 */
function eyvescloud_normalize_result($res)
{
    if (!is_array($res)) {
        return ['success' => false, 'message' => (string)$res, 'data' => []];
    }

    $ok = eyvescloud_success($res);
    $message = $res['msg'] ?? ($res['message'] ?? ($ok ? '操作成功' : '操作失败'));
    if (is_array($message)) {
        $message = reset($message);
    }
    $data = $res['data'] ?? [];

    $out = [
        'success' => (bool)$ok,
        'message' => (string)$message,
        'data'    => is_array($data) ? $data : [],
    ];
    if (isset($res['debug'])) {
        $out['debug'] = $res['debug'];
    }
    return $out;
}

/**
 * 客户区 AJAX 分发器：把前端的 action 映射到对应的面板封装函数。
 *
 * @param array  $params
 * @param string $action
 * @return array{success:bool,message:string,data:array}
 */
function eyvescloud_dispatch($params, $action)
{
    $action = trim((string)$action);

    // 统一把请求体并入 $_POST，保证 eyvescloud_request_value / eyvescloud_json_input 都能取到。
    $input = eyvescloud_json_input();
    if (is_array($input)) {
        foreach ($input as $key => $value) {
            if (!isset($_POST[$key])) {
                $_POST[$key] = $value;
            }
        }
    }

    switch ($action) {
        case 'info':
        case 'infoData':
        case 'infoajax':
            return eyvescloud_normalize_result(eyvescloud_infoData($params));

        case 'status':
            return eyvescloud_normalize_result(eyvescloud_container_status($params));

        case 'natList':
        case 'natData':
            return eyvescloud_normalize_result(eyvescloud_natList($params));

        case 'natajax':
            $name = strtolower(trim((string)eyvescloud_param_value($input, 'action', '')));
            if ($name === 'list') {
                return eyvescloud_normalize_result(eyvescloud_natList($params));
            }
            return eyvescloud_normalize_result(eyvescloud_nat_ajax($params));

        case 'addNat':
            return eyvescloud_normalize_result(eyvescloud_addNat($params));
        case 'updateNat':
            return eyvescloud_normalize_result(eyvescloud_updateNat($params));
        case 'deleteNat':
            return eyvescloud_normalize_result(eyvescloud_deleteNat($params));
        case 'randomPort':
        case 'random-port':
            return eyvescloud_normalize_result(eyvescloud_randomPort($params));

        case 'firewallList':
            return eyvescloud_normalize_result(eyvescloud_firewallList($params));
        case 'firewallUpdate':
            return eyvescloud_normalize_result(eyvescloud_firewallUpdate($params));
        case 'firewallajax':
            return eyvescloud_normalize_result(eyvescloud_firewall_ajax($params));

        case 'snapshotList':
            return eyvescloud_normalize_result(eyvescloud_snapshotList($params));
        case 'snapshotCreate':
            return eyvescloud_normalize_result(eyvescloud_snapshotCreate($params));
        case 'snapshotRestore':
            return eyvescloud_normalize_result(eyvescloud_snapshotRestore($params));
        case 'snapshotDelete':
            return eyvescloud_normalize_result(eyvescloud_snapshotDelete($params));

        case 'backupList':
            return eyvescloud_normalize_result(eyvescloud_backupList($params));
        case 'backupCreate':
            return eyvescloud_normalize_result(eyvescloud_backupCreate($params));
        case 'backupRestore':
            return eyvescloud_normalize_result(eyvescloud_backupRestore($params));
        case 'backupDelete':
            return eyvescloud_normalize_result(eyvescloud_backupDelete($params));

        case 'isoList':
            return eyvescloud_normalize_result(eyvescloud_isoList($params));
        case 'isoAttach':
            return eyvescloud_normalize_result(eyvescloud_isoAttach($params));
        case 'isoDetach':
            return eyvescloud_normalize_result(eyvescloud_isoDetach($params));

        case 'reinstallTemplates':
            return eyvescloud_normalize_result(eyvescloud_reinstallTemplates($params));
        case 'reinstall':
            return eyvescloud_normalize_result(eyvescloud_client_reinstall($params));

        case 'powerOn':
            return eyvescloud_normalize_result(eyvescloud_container_action($params, 'start', '开机任务已提交'));
        case 'powerOff':
            return eyvescloud_normalize_result(eyvescloud_container_action($params, 'stop', '关机任务已提交'));
        case 'powerReboot':
            return eyvescloud_normalize_result(eyvescloud_container_action($params, 'restart', '重启任务已提交'));

        case 'trafficReset':
            return eyvescloud_normalize_result(eyvescloud_traffic_reset($params));

        case 'sync':
            $res = eyvescloud_find_container($params);
            if (!eyvescloud_success($res) || empty($res['data'])) {
                return eyvescloud_normalize_result(['status' => 'error', 'msg' => eyvescloud_message($res, '同步失败')]);
            }
            eyvescloud_update_host_from_container($params, $res['data']);
            eyvescloud_set_expiry($params);
            return ['success' => true, 'message' => '状态已同步', 'data' => []];

        case 'webssh':
            return eyvescloud_normalize_result(eyvescloud_ssh_ticket($params));
        case 'vnc':
            return eyvescloud_normalize_result(eyvescloud_vnc_ticket($params));

        case '':
            return ['success' => false, 'message' => '缺少 action 参数', 'data' => []];
    }

    return ['success' => false, 'message' => '未知操作: ' . $action, 'data' => []];
}