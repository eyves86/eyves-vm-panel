<?php

use think\Db;

define('EYVESCLOUD_DEBUG', false);

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

function eyvescloud_json_response($payload)
{
    if (!headers_sent()) {
        header('Content-Type: application/json; charset=utf-8');
    }
    echo json_encode($payload, JSON_UNESCAPED_UNICODE);
    exit;
}

function eyvescloud_MetaData()
{
    return [
        'DisplayName' => 'EYVESCLOUD 计费系统对接模块',
        'APIVersion'  => '1.1',
        'HelpDoc'     => 'https://github.com/FenhaoLost/eyves-vm-panel',
        'version'     => '1.0.12',
    ];
}

function eyvescloud_ConfigOptions()
{
    return [
        ['type' => 'dropdown', 'name' => '虚拟化类型', 'description' => 'lxc 或 kvm', 'default' => 'lxc', 'key' => 'virtualization', 'options' => ['lxc' => 'LXC', 'kvm' => 'KVM']],
        ['type' => 'text', 'name' => '镜像/模板 ID', 'description' => 'EYVESCLOUD 模板 ID，例如 alpine-3.21、debian-bookworm、ubuntu-jammy 或已启用的 KVM 镜像 ID', 'default' => 'alpine-3.21', 'key' => 'template_id'],
        ['type' => 'text', 'name' => 'CPU 核心', 'description' => 'vCPU 数量，KVM 必须为整数', 'default' => '1', 'key' => 'vcpu'],
        ['type' => 'text', 'name' => 'CPU 百分比', 'description' => 'CPU 使用率限制，0 表示不额外限制', 'default' => '0', 'key' => 'cpu_percent'],
        ['type' => 'text', 'name' => '内存 MB', 'description' => '容器内存，单位 MB', 'default' => '512', 'key' => 'ram_mb'],
        ['type' => 'text', 'name' => '硬盘 GB', 'description' => '系统盘大小，单位 GB（支持 0.5、0.75、1、5 等浮点数）', 'default' => '5', 'key' => 'disk_gb'],
        ['type' => 'text', 'name' => '带宽 Mbps', 'description' => '网络带宽限制，0 表示不限制', 'default' => '100', 'key' => 'network_bw_mbps'],
        ['type' => 'dropdown', 'name' => '流量模式', 'description' => 'total=总流量，in_out=分别限制入/出方向', 'default' => 'total', 'key' => 'traffic_mode', 'options' => ['total' => '总流量', 'in_out' => '入/出分开']],
        ['type' => 'text', 'name' => '月流量 GB', 'description' => 'total 模式下的月流量限制，0 表示不限制', 'default' => '100', 'key' => 'monthly_traffic_gb'],
        ['type' => 'text', 'name' => '入站流量 GB', 'description' => 'in_out 模式下入站流量限制，0 表示不限制', 'default' => '0', 'key' => 'traffic_in_gb'],
        ['type' => 'text', 'name' => '出站流量 GB', 'description' => 'in_out 模式下出站流量限制，0 表示不限制', 'default' => '0', 'key' => 'traffic_out_gb'],
        ['type' => 'text', 'name' => 'IO 速度 MB/s', 'description' => '磁盘 IO 限制，0 表示不限制', 'default' => '0', 'key' => 'io_speed_mbps'],
        ['type' => 'dropdown', 'name' => '分配 NAT', 'description' => '开通时是否分配 NAT 端口映射', 'default' => 'true', 'key' => 'assign_nat', 'options' => ['true' => '启用', 'false' => '禁用']],
        ['type' => 'text', 'name' => 'NAT 端口数量', 'description' => '开通时分配的端口映射数量，最小 2', 'default' => '2', 'key' => 'port_mapping_count'],
        ['type' => 'text', 'name' => '快照配额', 'description' => '每台实例允许保留的快照数量', 'default' => '3', 'key' => 'snapshot_limit'],
        ['type' => 'text', 'name' => '额外端口', 'description' => '逗号分隔的容器端口，例如 80,443', 'default' => '', 'key' => 'extra_ports'],
        ['type' => 'dropdown', 'name' => '自动公网 IPv4', 'description' => '开通时是否从 EYVESCLOUD 公网 IPv4 池分配独立 IPv4', 'default' => 'false', 'key' => 'assign_ipv4', 'options' => ['true' => '启用', 'false' => '禁用']],
        ['type' => 'text', 'name' => '公网 IPv4 数量', 'description' => '自动分配公网 IPv4 的数量，通常填写 1', 'default' => '1', 'key' => 'ipv4_count'],
        ['type' => 'text', 'name' => '指定公网 IPv4', 'description' => '指定分配的公网 IPv4，多个用逗号分隔；留空则从地址池自动分配', 'default' => '', 'key' => 'public_ipv4s'],
        ['type' => 'dropdown', 'name' => '自动 IPv6', 'description' => '开通时自动分配 IPv6', 'default' => 'false', 'key' => 'assign_ipv6', 'options' => ['true' => '启用', 'false' => '禁用']],
        ['type' => 'text', 'name' => 'IPv6 数量', 'description' => '自动分配 IPv6 的数量，通常填写 1', 'default' => '1', 'key' => 'ipv6_count'],
        ['type' => 'text', 'name' => '指定 IPv6', 'description' => '指定分配的 IPv6 地址，多个用逗号分隔；留空则从地址池自动分配', 'default' => '', 'key' => 'ipv6_addresses'],
        ['type' => 'dropdown', 'name' => 'SSH 鉴权模式', 'description' => 'auto_password=自动生成密码，password=使用指定密码，key=使用 SSH 公钥', 'default' => 'auto_password', 'key' => 'ssh_auth_mode', 'options' => ['auto_password' => '自动密码', 'password' => '指定密码', 'key' => 'SSH 公钥']],
        ['type' => 'text', 'name' => '指定 SSH 密码', 'description' => 'SSH 鉴权模式为 password 时使用；其他模式留空', 'default' => '', 'key' => 'ssh_password'],
        ['type' => 'text', 'name' => 'SSH 公钥', 'description' => 'SSH 鉴权模式为 key 时使用；填写完整 public key', 'default' => '', 'key' => 'ssh_public_key'],
        ['type' => 'dropdown', 'name' => '同步到期时间', 'description' => '开通/续费时把计费系统到期日期同步到 EYVESCLOUD，格式会转换为 YYYY-MM-DD', 'default' => 'true', 'key' => 'sync_expiry', 'options' => ['true' => '启用', 'false' => '禁用']],
    ];
}

function eyvescloud_base_url($params)
{
    if (!empty($params['server_host'])) {
        return rtrim($params['server_host'], '/');
    }

    $host = $params['server_ip'] ?? $params['ip'] ?? '';
    $port = $params['port'] ?? '';
    $scheme = (!empty($params['secure']) && (string)$params['secure'] !== '0') ? 'https' : 'http';

    if (stripos($host, 'http://') === 0 || stripos($host, 'https://') === 0) {
        $base = rtrim($host, '/');
    } else {
        $base = $scheme . '://' . $host;
    }

    if ($port !== '' && strpos(parse_url($base, PHP_URL_HOST) ?: $base, ':') === false) {
        $base .= ':' . $port;
    }

    return rtrim($base, '/');
}

function eyvescloud_api_key($params)
{
    foreach (['accesshash', 'server_password', 'password'] as $key) {
        if (!empty($params[$key])) {
            return trim($params[$key]);
        }
    }
    return '';
}

function eyvescloud_request($params, $endpoint, $data = [], $method = 'GET', $timeout = 30, $extraHeaders = [])
{
    $url = eyvescloud_base_url($params) . $endpoint;
    $apiKey = eyvescloud_api_key($params);
    $method = strtoupper($method);
    // 默认开启 TLS 证书校验；仅当服务器配置里显式设置 insecure=1 时允许自签证书跳过校验。
    $insecure = !empty($params['insecure']);

    $curl = curl_init();
    $headers = [
        'Content-Type: application/json',
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
        CURLOPT_USERAGENT      => 'Mofang-EYVESCLOUD',
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

    $decoded = json_decode($body, true);
    if (!is_array($decoded)) {
        return ['success' => false, 'message' => 'Invalid JSON response: ' . substr((string)$body, 0, 300), '_http_code' => $httpCode];
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

function eyvescloud_success($res)
{
    if (!is_array($res)) {
        return false;
    }
    if (isset($res['success'])) {
        return (bool)$res['success'];
    }
    return isset($res['code']) && (int)$res['code'] >= 200 && (int)$res['code'] < 300;
}

function eyvescloud_message($res, $fallback = '操作失败')
{
    if (!is_array($res)) {
        return $fallback;
    }
    return $res['message'] ?? $res['msg'] ?? $res['error'] ?? $fallback;
}

function eyvescloud_container_name($params)
{
    $name = $params['domain'] ?? '';
    if (is_array($name)) {
        $name = reset($name);
    }
    $name = trim((string)$name);
    if ($name === '') {
        $name = 'host-' . ($params['hostid'] ?? time());
    }
    $name = preg_replace('/[^A-Za-z0-9_.-]/', '-', $name);
    return trim($name, '-.');
}

function eyvescloud_host_id($params)
{
    foreach (['hostid', 'id', 'serviceid', 'service_id', 'relid'] as $key) {
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

    $value = trim((string)$value);
    return $value;
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
        return '';
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

    foreach (['server_ip', 'ip'] as $key) {
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

function eyvescloud_store_password($password)
{
    $password = (string)$password;
    if ($password === '') {
        return '';
    }
    return function_exists('cmf_encrypt') ? cmf_encrypt($password) : $password;
}

function eyvescloud_webssh_url($params, $ticket, $containerName)
{
    $baseUrl = rtrim(eyvescloud_base_url($params), '/');
    $scheme = stripos($baseUrl, 'https://') === 0 ? 'wss' : 'ws';
    $host = parse_url($baseUrl, PHP_URL_HOST);
    $port = parse_url($baseUrl, PHP_URL_PORT);
    $wsBase = $scheme . '://' . $host . ($port ? ':' . $port : '');
    $wsUrl = $wsBase
        . '/api/ssh?container=' . rawurlencode((string)$containerName)
        . '&container_name=' . rawurlencode((string)$containerName)
        . '&ticket=' . rawurlencode((string)$ticket);

    $siteScheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $siteHost = $_SERVER['HTTP_HOST'] ?? '';
    $handler = ($siteHost !== '' ? $siteScheme . '://' . $siteHost : '') . '/plugins/servers/eyvescloud/handlers/webssh.php';

    return $handler
        . '?ws=' . rawurlencode($wsUrl)
        . '&protocol=' . rawurlencode('eyvescloud-ticket.' . (string)$ticket)
        . '&ticket=' . rawurlencode((string)$ticket)
        . '&container=' . rawurlencode((string)$containerName);
}

function eyvescloud_vnc_url($params, $ticket, $containerName)
{
    $baseUrl = rtrim(eyvescloud_base_url($params), '/');
    $scheme = stripos($baseUrl, 'https://') === 0 ? 'wss' : 'ws';
    $host = parse_url($baseUrl, PHP_URL_HOST);
    $port = parse_url($baseUrl, PHP_URL_PORT);
    $wsBase = $scheme . '://' . $host . ($port ? ':' . $port : '');
    // VNC WebSocket 代理固定为 /api/vnc（注意：不带 /v1），见 backend/internal/server/server.go。
    // 票据通过 WebSocket 子协议 eyvescloud-vnc-ticket.<ticket> 传递。
    $wsUrl = $wsBase . '/api/vnc?container=' . rawurlencode((string)$containerName);

    $siteScheme = (!empty($_SERVER['HTTPS']) && $_SERVER['HTTPS'] !== 'off') ? 'https' : 'http';
    $siteHost = $_SERVER['HTTP_HOST'] ?? '';
    $handler = ($siteHost !== '' ? $siteScheme . '://' . $siteHost : '') . '/plugins/servers/eyvescloud/handlers/vnc.php';

    return $handler
        . '?ws=' . rawurlencode($wsUrl)
        . '&protocol=' . rawurlencode('eyvescloud-vnc-ticket.' . (string)$ticket)
        . '&ticket=' . rawurlencode((string)$ticket)
        . '&container=' . rawurlencode((string)$containerName);
}

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
    if (is_array($value)) {
        $parts = $value;
    } else {
        $parts = preg_split('/[,;\s]+/', (string)$value);
    }

    $result = [];
    foreach ($parts as $part) {
        $part = trim((string)$part);
        if ($part !== '') {
            $result[] = $part;
        }
    }
    return array_values(array_unique($result));
}

function eyvescloud_expiry_from_params($params)
{
    $options = $params['configoptions'] ?? [];
    if (!eyvescloud_bool_option($options['sync_expiry'] ?? 'true', true)) {
        return '';
    }
    $raw = $params['nextduedate'] ?? '';
    if ($raw === '' || $raw === '0' || $raw === 0 || $raw === '0000-00-00' || $raw === '0000-00-00 00:00:00') {
        return '';
    }

    $timestamp = 0;
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

function eyvescloud_container_payload($params)
{
    $options = $params['configoptions'] ?? [];
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
 * EYVESCLOUD 的开通/重装是异步任务队列，POST 返回时容器可能尚未创建，
 * 或 ssh_port / ssh_password / status 仍为空。这里轮询任务队列与容器详情，
 * 直到容器就绪或超时。超时不视为失败，而是尽力写回已有字段。
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

    $msg = '容器仍在初始化，字段可能稍后才可用';
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

function eyvescloud_post_value($key, $default = '')
{
    if (function_exists('input')) {
        $value = input('post.' . $key);
        return $value === null ? $default : $value;
    }
    return $_POST[$key] ?? $default;
}

function eyvescloud_request_value($key, $default = '')
{
    if (function_exists('input')) {
        $value = input('param.' . $key);
        if ($value === null) {
            $value = input('*.' . $key);
        }
        return $value === null ? $default : $value;
    }
    if (isset($_POST[$key])) {
        return $_POST[$key];
    }
    return $_GET[$key] ?? $default;
}

function eyvescloud_json_input()
{
    $input = [];
    if (!empty($_POST) && is_array($_POST)) {
        $input = $_POST;
    }

    $raw = file_get_contents('php://input');
    $data = json_decode((string)$raw, true);
    if (is_array($data)) {
        return array_merge($input, $data);
    }

    $form = [];
    parse_str((string)$raw, $form);
    if (!empty($form) && is_array($form)) {
        return array_merge($input, $form);
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
            'tcp_selected'   => $protocol === 'udp' ? '' : 'selected',
            'udp_selected'   => $protocol === 'udp' ? 'selected' : '',
            'description'    => $mapping['description'] ?? '',
        ];
    }

    return $result;
}

function eyvescloud_nat_post_action()
{
    $func = eyvescloud_request_value('func', '');
    return strtolower(trim((string)$func));
}

function eyvescloud_handle_nat_post($params)
{
    $action = eyvescloud_nat_post_action();
    if ($action === '') {
        return ['message' => '', 'mappings' => null];
    }

    if (!in_array($action, ['randomport', 'addnat', 'updatenat', 'deletenat'], true)) {
        return ['message' => '', 'mappings' => null];
    }

    $map = [
        'randomport' => 'eyvescloud_randomPort',
        'addnat'     => 'eyvescloud_addNat',
        'updatenat'  => 'eyvescloud_updateNat',
        'deletenat'  => 'eyvescloud_deleteNat',
    ];

    if (!isset($map[$action]) || !function_exists($map[$action])) {
        return ['message' => '', 'mappings' => null];
    }

    $res = call_user_func($map[$action], $params);
    if (!is_array($res)) {
        return ['message' => (string)$res, 'mappings' => null];
    }

    $ok = (($res['status'] ?? '') === 'success' || (int)($res['status'] ?? 0) === 200);
    $prefix = $ok ? '成功: ' : '失败: ';
    return [
        'message'  => $prefix . ($res['msg'] ?? '操作完成'),
        'mappings' => ($ok && isset($res['data']['port_mappings']) && is_array($res['data']['port_mappings'])) ? $res['data']['port_mappings'] : null,
    ];
}

function eyvescloud_nat_payload_from_post()
{
    return eyvescloud_nat_payload_from_data(null);
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

function eyvescloud_nat_ajax($params)
{
    $input = eyvescloud_json_input();
    $action = strtolower(trim((string)eyvescloud_param_value($input, 'action', '')));
    $debug = [eyvescloud_debug_entry('NAT ajax received', [
        'action' => $action,
        'input'  => $input,
        'query'  => $_GET,
    ])];

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
            ? ['status' => 'success', 'msg' => '随机端口: ' . ($res['data']['port'] ?? ''), 'port' => $res['data']['port'] ?? '', 'debug' => $debug]
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
        'status'        => 'success',
        'msg'           => eyvescloud_message($res, 'NAT 操作成功'),
        'port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? []),
        'debug'         => $debug,
    ];
}

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
    if (!eyvescloud_success($usageCall['response']) && !empty($c['uuid'])) {
        $usageCall = eyvescloud_request_debug($params, '/api/containers/' . rawurlencode((string)$c['uuid']) . '/usage', [], 'GET', 30);
    }
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
    $options = $params['configoptions'] ?? [];
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

    return [
        'status' => 'success',
        'data'   => [
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
            'traffic_used_text' => eyvescloud_format_bytes($rxBytes + $txBytes),
            'traffic_limit_text'=> $limitGB > 0 ? round($limitGB, 2) . ' GB' : '不限',
            'traffic_in_text'   => eyvescloud_format_bytes($rxBytes),
            'traffic_out_text'  => eyvescloud_format_bytes($txBytes),
            'traffic_percent'=> $trafficPercent,
            'net_in_bps'     => round($netInBps, 2),
            'net_out_bps'    => round($netOutBps, 2),
            'net_in_rate'    => eyvescloud_format_rate($netInBps),
            'net_out_rate'   => eyvescloud_format_rate($netOutBps),
            'disk_read_bps'  => round($diskReadBps, 2),
            'disk_write_bps' => round($diskWriteBps, 2),
            'disk_read_rate' => eyvescloud_format_rate($diskReadBps),
            'disk_write_rate'=> eyvescloud_format_rate($diskWriteBps),
            'chart_time'     => date('H:i:s'),
            'usage'          => $usage,
            'history'        => $history,
        ],
        'debug'  => $debug,
    ];
}

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

function eyvescloud_update_host_from_container($params, $container)
{
    $hostId = eyvescloud_host_id($params);
    if ($hostId <= 0 || !is_array($container)) {
        return;
    }

    $update = [
        'domainstatus' => eyvescloud_domain_status_from_container($container),
        'username'     => 'root',
        'dedicatedip'  => eyvescloud_public_host($params, $container, true),
    ];

    $sshPort = eyvescloud_container_ssh_port($container);
    if ($sshPort !== '') {
        $update['port'] = $sshPort;
    }

    $password = eyvescloud_container_password($container);
    if ($password !== '') {
        $update['password'] = eyvescloud_store_password($password);
    }

    try {
        Db::name('host')->where('id', $hostId)->update($update);
    } catch (\Exception $e) {
        eyvescloud_debug('host update failed', $e->getMessage());
    }
}

function eyvescloud_TestLink($params)
{
    $res = eyvescloud_request($params, '/api/v1/dashboard', [], 'GET');
    return [
        'status' => 200,
        'data'   => [
            'server_status' => eyvescloud_success($res) ? 1 : 0,
            'msg'           => eyvescloud_success($res) ? '连接成功' : eyvescloud_message($res, '连接失败'),
        ],
    ];
}

function eyvescloud_CreateAccount($params)
{
    $exists = eyvescloud_find_container($params);
    if (eyvescloud_success($exists)) {
        return ['status' => 'error', 'msg' => '容器已存在，不能重复开通'];
    }

    $payload = eyvescloud_container_payload($params);
    if (empty($payload['template_id'])) {
        return ['status' => 'error', 'msg' => '产品配置缺少 template_id'];
    }

    // 幂等键：同一主机重试开通时，后端返回既有容器（而不是二次开通），
    // 与容器名唯一校验共同兜底，防止计费系统回调超时后重复开通。
    $idemKey = 'container-create-' . eyvescloud_host_id($params);
    $res = eyvescloud_request($params, '/api/v1/containers', $payload, 'POST', 120, ['Idempotency-Key' => $idemKey]);
    if (!eyvescloud_success($res)) {
        // 幂等命中（后端返回已存在容器）视为开通成功，避免误报失败。
        $message = (string)eyvescloud_message($res, '');
        if (stripos($message, 'idempotent') === false && stripos($message, 'already exists') === false) {
            return ['status' => 'error', 'msg' => eyvescloud_message($res, '开通失败')];
        }
    }

    $hostId = eyvescloud_host_id($params);
    if ($hostId > 0) {
        try {
            Db::name('host')->where('id', $hostId)->update([
                'domainstatus' => 'Active',
                'username'     => 'root',
                'dedicatedip'  => eyvescloud_public_ipv4_from_routing($params) ?: eyvescloud_public_host($params),
            ]);
        } catch (\Exception $e) {
            return ['status' => 'error', 'msg' => '开通成功但同步计费系统数据库失败: ' . $e->getMessage()];
        }
    }

    $wait = eyvescloud_wait_container_ready($params);
    if (!empty($wait['container'])) {
        eyvescloud_update_host_from_container($params, $wait['container']);
    }

    $msg = eyvescloud_message($res, '开通成功');
    if (!empty($wait['failed'])) {
        return ['status' => 'error', 'msg' => $msg . '，但' . ($wait['msg'] ?? '异步任务失败')];
    }
    if (empty($wait['ready'])) {
        $msg .= '（' . ($wait['msg'] ?? '容器仍在初始化') . '）';
    }

    return ['status' => 'success', 'msg' => $msg];
}

function eyvescloud_TerminateAccount($params)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/delete', [], 'DELETE', 60);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '删除任务已提交')]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '删除失败')];
}

function eyvescloud_action($params, $action, $successMsg, $timeout = 60)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/' . $action, [], 'POST', $timeout);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, $successMsg)]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, $successMsg . '失败')];
}

function eyvescloud_On($params)
{
    return eyvescloud_action($params, 'start', '开机任务已提交');
}

function eyvescloud_Off($params)
{
    return eyvescloud_action($params, 'stop', '关机任务已提交');
}

function eyvescloud_Reboot($params)
{
    return eyvescloud_action($params, 'restart', '重启任务已提交');
}

function eyvescloud_SuspendAccount($params)
{
    return eyvescloud_Off($params);
}

function eyvescloud_UnsuspendAccount($params)
{
    return eyvescloud_On($params);
}

function eyvescloud_Status($params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '查询失败')];
    }

    $status = strtolower($res['data']['status'] ?? '');
    if ($status === 'running') {
        return ['status' => 'success', 'data' => ['status' => 'on', 'des' => '运行中']];
    }
    if ($status === 'stopped') {
        return ['status' => 'success', 'data' => ['status' => 'off', 'des' => '已关机']];
    }
    return ['status' => 'success', 'data' => ['status' => 'unknown', 'des' => $status ?: '未知']];
}

function eyvescloud_Sync($params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '同步失败')];
    }
    eyvescloud_update_host_from_container($params, $res['data']);
    return ['status' => 'success', 'msg' => '同步成功'];
}

function eyvescloud_Reinstall($params)
{
    $templateId = $params['reinstall_os'] ?? '';
    if ($templateId === '') {
        $templateId = ($params['configoptions']['template_id'] ?? '');
    }
    if ($templateId === '') {
        return ['status' => 'error', 'msg' => '缺少重装系统模板 ID'];
    }

    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/reinstall', ['template_id' => $templateId], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '重装失败')];
    }

    $wait = eyvescloud_wait_container_ready($params);
    if (!empty($wait['container'])) {
        eyvescloud_update_host_from_container($params, $wait['container']);
    } elseif (isset($res['data']) && is_array($res['data'])) {
        eyvescloud_update_host_from_container($params, $res['data']);
    }

    $msg = eyvescloud_message($res, '重装任务已提交');
    if (!empty($wait['failed'])) {
        return ['status' => 'error', 'msg' => $msg . '，但' . ($wait['msg'] ?? '异步任务失败')];
    }
    if (empty($wait['ready'])) {
        $msg .= '（' . ($wait['msg'] ?? '容器仍在初始化') . '）';
    }

    return ['status' => 'success', 'msg' => $msg];
}

function eyvescloud_CrackPassword($params, $new_pass)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/reset-password', ['password' => $new_pass], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '重置密码失败')];
    }

    $password = $res['data']['ssh_password'] ?? $res['data']['password'] ?? $new_pass;
    $hostId = eyvescloud_host_id($params);
    if ($hostId > 0) {
        try {
            Db::name('host')->where('id', $hostId)->update(['password' => eyvescloud_store_password($password)]);
            $detail = eyvescloud_find_container($params);
            if (eyvescloud_success($detail) && isset($detail['data'])) {
                eyvescloud_update_host_from_container($params, $detail['data']);
            }
        } catch (\Exception $e) {
            return ['status' => 'error', 'msg' => '密码重置成功但同步计费系统数据库失败: ' . $e->getMessage()];
        }
    }

    return ['status' => 'success', 'msg' => eyvescloud_message($res, '密码重置成功')];
}

// WHMCS / 魔方财务客户改密码标准入口：从 $params['password'] 取新密码后委托给 CrackPassword。
function eyvescloud_ChangePassword($params)
{
    $newPass = $params['password'] ?? '';
    if (is_array($newPass)) {
        $newPass = reset($newPass);
    }
    $newPass = trim((string)$newPass);
    if ($newPass === '') {
        return ['status' => 'error', 'msg' => '新密码不能为空'];
    }

    return eyvescloud_CrackPassword($params, $newPass);
}

function eyvescloud_TrafficReset($params)
{
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic-reset', [], 'POST', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => eyvescloud_message($res, '流量已重置')]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '流量重置失败')];
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
    return ['status' => 200, 'msg' => $port ? '随机端口: ' . $port : '随机端口获取成功', 'data' => ['port' => $port]];
}

function eyvescloud_addNat($params)
{
    $payload = eyvescloud_nat_payload_from_post();
    if (isset($payload['error'])) {
        return ['status' => 'error', 'msg' => $payload['error']];
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings', $payload, 'POST', 30);
    return eyvescloud_success($res)
        ? ['status' => 200, 'msg' => eyvescloud_message($res, '端口映射添加成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '端口映射添加失败')];
}

function eyvescloud_updateNat($params)
{
    $index = eyvescloud_request_value('index', '');
    if ($index === '' || !is_numeric($index) || (int)$index < 0) {
        return ['status' => 'error', 'msg' => '端口映射索引错误'];
    }

    $payload = eyvescloud_nat_payload_from_post();
    if (isset($payload['error'])) {
        return ['status' => 'error', 'msg' => $payload['error']];
    }

    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $endpoint = '/api/v1/containers/' . rawurlencode($containerId) . '/port-mappings/' . rawurlencode((string)(int)$index);
    $res = eyvescloud_request($params, $endpoint, $payload, 'PUT', 30);
    return eyvescloud_success($res)
        ? ['status' => 200, 'msg' => eyvescloud_message($res, '端口映射更新成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
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
        ? ['status' => 200, 'msg' => eyvescloud_message($res, '端口映射删除成功'), 'data' => ['port_mappings' => eyvescloud_normalize_port_mappings($res['data'] ?? [])]]
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '端口映射删除失败')];
}

function eyvescloud_natList($params)
{
    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data']) || !is_array($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取 NAT 列表失败')];
    }

    return [
        'status' => 200,
        'msg'    => '获取成功',
        'data'   => [
            'port_mappings' => eyvescloud_normalize_port_mappings(eyvescloud_port_mappings_from_container($res['data'])),
            'debug' => [
                eyvescloud_debug_entry('NatList', [
                    'container' => [
                        'id'   => $res['data']['id'] ?? null,
                        'uuid' => $res['data']['uuid'] ?? null,
                        'name' => $res['data']['name'] ?? null,
                    ],
                    'http' => $res['_http_code'] ?? null,
                ]),
            ],
        ],
    ];
}

function eyvescloud_infoData($params)
{
    $data = eyvescloud_info_ajax($params);
    if (($data['status'] ?? '') !== 'success') {
        return [
            'status' => 200,
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
                'debug'          => $data['debug'] ?? [],
            ],
        ];
    }

    $data['data']['debug'] = $data['debug'] ?? [];
    return ['status' => 200, 'msg' => '获取成功', 'data' => $data['data']];
}

function eyvescloud_ChangePackage($params)
{
    $options = $params['configoptions'] ?? [];
    $name = eyvescloud_container_name($params);

    $resource = [
        'vcpu'             => eyvescloud_float_option($options, 'vcpu', 0),
        'ram_mb'           => eyvescloud_int_option($options, 'ram_mb', 0),
        'disk_gb'          => eyvescloud_float_option($options, 'disk_gb', 0),
        'io_speed_mbps'    => eyvescloud_int_option($options, 'io_speed_mbps', 0),
        'network_bw_mbps'  => eyvescloud_int_option($options, 'network_bw_mbps', 0),
    ];
    $resource = array_filter($resource, function ($value) {
        return $value !== 0 && $value !== 0.0;
    });

    if (!empty($resource)) {
        $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/resource-limit', $resource, 'PUT', 60);
        if (!eyvescloud_success($res)) {
            return ['status' => 'error', 'msg' => eyvescloud_message($res, '资源限制调整失败')];
        }
    }

    $traffic = [
        'traffic_mode'       => $options['traffic_mode'] ?? 'total',
        'monthly_traffic_gb' => eyvescloud_int_option($options, 'monthly_traffic_gb', 0),
        'traffic_in_gb'      => eyvescloud_int_option($options, 'traffic_in_gb', 0),
        'traffic_out_gb'     => eyvescloud_int_option($options, 'traffic_out_gb', 0),
    ];
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic-limit', $traffic, 'PUT', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '流量限制调整失败')];
    }

    $expiresAt = eyvescloud_expiry_from_params($params);
    if ($expiresAt !== '') {
        $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/expiry', ['expires_at' => $expiresAt], 'PUT', 30);
        if (!eyvescloud_success($res)) {
            return ['status' => 'error', 'msg' => eyvescloud_message($res, '到期时间同步失败')];
        }
    }

    return ['status' => 'success', 'msg' => '配置变更成功'];
}

function eyvescloud_Renew($params)
{
    $expiresAt = eyvescloud_expiry_from_params($params);
    if ($expiresAt === '') {
        return ['status' => 'success', 'msg' => '未启用到期时间同步'];
    }
    $name = eyvescloud_container_name($params);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/expiry', ['expires_at' => $expiresAt], 'PUT', 30);
    return eyvescloud_success($res)
        ? ['status' => 'success', 'msg' => '续费到期时间同步成功']
        : ['status' => 'error', 'msg' => eyvescloud_message($res, '续费同步失败')];
}

function eyvescloud_UsageUpdate($params)
{
    $name = eyvescloud_container_name($params);
    $usageRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/usage', [], 'GET', 30);
    $trafficRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($name) . '/traffic', [], 'GET', 30);

    $usage = eyvescloud_success($usageRes) && isset($usageRes['data']) && is_array($usageRes['data']) ? $usageRes['data'] : [];
    $traffic = eyvescloud_success($trafficRes) && isset($trafficRes['data']) && is_array($trafficRes['data']) ? $trafficRes['data'] : [];
    if (empty($usage) && empty($traffic)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($trafficRes, eyvescloud_message($usageRes, '获取用量失败'))];
    }

    $hostId = eyvescloud_host_id($params);
    if ($hostId <= 0) {
        return ['status' => 'success', 'msg' => '用量获取成功（无主机记录可写回）'];
    }

    $rxBytes = eyvescloud_pick_number([$traffic, $usage], ['rx_used_bytes', 'traffic_used_rx', 'rx_bytes', 'network_rx_bytes'], 0);
    $txBytes = eyvescloud_pick_number([$traffic, $usage], ['tx_used_bytes', 'traffic_used_tx', 'tx_bytes', 'network_tx_bytes'], 0);
    $totalBytes = eyvescloud_pick_number([$traffic], ['total_used_bytes'], 0);
    if ($totalBytes <= 0) {
        $totalBytes = $rxBytes + $txBytes;
    }
    $limitGB = eyvescloud_pick_number([$traffic], ['limit_gb', 'monthly_traffic_gb'], 0);

    // 计费系统主机表流量用量字段：魔方财务沿用 WHMCS 的 bwusage / bwlimit（单位 MB）。
    // TODO: 仓库内无该字段定义可核对，若目标计费系统字段名或单位不同，请按实际调整。
    $update = [
        'bwusage' => round($totalBytes / 1048576, 2),
        'bwlimit' => $limitGB > 0 ? round($limitGB * 1024, 2) : 0,
    ];

    try {
        Db::name('host')->where('id', $hostId)->update($update);
    } catch (\Exception $e) {
        return ['status' => 'error', 'msg' => '用量写回失败: ' . $e->getMessage()];
    }

    return ['status' => 'success', 'msg' => '用量已更新'];
}

function eyvescloud_AdminButton($params)
{
    if (empty($params['domain'])) {
        return [];
    }
    return [
        'Sync'         => '同步状态',
        'TrafficReset' => '重置流量',
        'vnc'          => 'VNC 控制台',
    ];
}

function eyvescloud_ClientButton($params)
{
    if (empty($params['domain'])) {
        return [];
    }
    return [
        'webssh' => [
            'place' => 'console',
            'name'  => 'WebSSH',
        ],
        'vnc' => [
            'place' => 'console',
            'name'  => 'WebVNC',
        ],
    ];
}

function eyvescloud_webssh($params)
{
    $container = [];
    $containerName = eyvescloud_container_name($params);
    eyvescloud_container_api_id($params, $container);
    if (!empty($container['name'])) {
        $containerName = (string)$container['name'];
    }

    $res = eyvescloud_request($params, '/api/v1/ssh-ticket', ['container_name' => $containerName], 'POST', 30);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, 'WebSSH ticket create failed')];
    }

    $ticket = $res['data']['ticket'] ?? '';
    if ($ticket === '') {
        return ['status' => 'error', 'msg' => 'WebSSH ticket is empty'];
    }

    $url = eyvescloud_webssh_url($params, $ticket, $containerName);
    $jsUrl = json_encode($url, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

    return [
        'status' => 'success',
        'msg'    => "WebSSH started<script type='text/javascript'>window.open({$jsUrl}, '_blank');</script>",
    ];
}

function eyvescloud_vnc($params)
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

    $url = eyvescloud_vnc_url($params, $ticket, $containerName);
    $jsUrl = json_encode($url, JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE);

    return [
        'status' => 'success',
        'msg'    => "WebVNC started<script type='text/javascript'>window.open({$jsUrl}, '_blank');</script>",
    ];
}

function eyvescloud_firewallList($params)
{
    $container = [];
    $containerId = eyvescloud_container_api_id($params, $container);
    $res = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', [], 'GET', 30);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取防火墙设置失败')];
    }

    return [
        'status' => 200,
        'msg'    => '获取成功',
        'data'   => $res['data'],
    ];
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

    // GET after PUT to confirm the actual state after EYVESCLOUD processes it
    $getRes = eyvescloud_request($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', [], 'GET', 30);
    $actualData = [];
    if (eyvescloud_success($getRes) && !empty($getRes['data']) && is_array($getRes['data'])) {
        $actualData = $getRes['data'];
    }

    return [
        'status' => 200,
        'msg'    => eyvescloud_message($res, '防火墙设置已更新'),
        'data'   => $actualData,
    ];
}

function eyvescloud_firewall_ajax($params)
{
    $input = eyvescloud_json_input();
    $action = strtolower(trim((string)eyvescloud_param_value($input, 'action', '')));
    $debug = [eyvescloud_debug_entry('Firewall ajax received', [
        'action' => $action,
        'input'  => $input,
        'query'  => $_GET,
    ])];

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

    if (!in_array($action, ['list', 'update'], true)) {
        return ['status' => 'error', 'msg' => '未知防火墙操作', 'debug' => $debug];
    }

    if ($action === 'list') {
        $call = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', [], 'GET', 30);
        $debug[] = $call['debug'];
        $res = $call['response'];
        if (!eyvescloud_success($res) || empty($res['data'])) {
            return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取防火墙设置失败'), 'debug' => $debug];
        }
        return [
            'status' => 'success',
            'msg'    => '获取成功',
            'data'   => $res['data'],
            'debug'  => $debug,
        ];
    }

    // update
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

    $call = eyvescloud_request_debug($params, '/api/v1/containers/' . rawurlencode($containerId) . '/firewall', $payload, 'PUT', 30);
    $debug[] = $call['debug'];
    $res = $call['response'];

    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '更新防火墙设置失败'), 'debug' => $debug];
    }

    return [
        'status' => 'success',
        'msg'    => eyvescloud_message($res, '防火墙设置已更新'),
        'data'   => $res['data'] ?? [],
        'debug'  => $debug,
    ];
}

// ---- 通用：把后端返回的资产列表转成客户区友好的展示字段 ----
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

// ---- 快照：客户区（list / create / restore / delete） ----
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
    return ['status' => 200, 'msg' => '获取成功', 'data' => [
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
    return ['status' => 200, 'msg' => '快照已创建'];
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
    return ['status' => 200, 'msg' => '还原任务已提交'];
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
    return ['status' => 200, 'msg' => '快照已删除'];
}

// ---- 备份：客户区（list / create / restore / delete） ----
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
    return ['status' => 200, 'msg' => '获取成功', 'data' => ['backups' => eyvescloud_normalize_assets($res['data'])]];
}

function eyvescloud_backupCreate($params)
{
    $res = eyvescloud_request($params, eyvescloud_backup_base($params), [], 'POST', 180);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '创建备份失败')];
    }
    return ['status' => 200, 'msg' => '备份已创建'];
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
    return ['status' => 200, 'msg' => '还原任务已提交'];
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
    return ['status' => 200, 'msg' => '备份已删除'];
}

// ---- ISO 挂载：客户区（KVM only）----
function eyvescloud_isoList($params)
{
    $res = eyvescloud_request($params, '/api/isos', [], 'GET', 30);
    if (!eyvescloud_success($res) || !isset($res['data'])) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '获取 ISO 列表失败')];
    }
    return ['status' => 200, 'msg' => '获取成功', 'data' => ['isos' => eyvescloud_normalize_assets($res['data'])]];
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
        return ['status' => 'error', 'msg' => '无法解析容器编号，请用容器名称产品'];
    }
    $res = eyvescloud_request($params, '/api/isos/attach', ['container_id' => $cid, 'iso_id' => $isoId, 'attach' => true], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '挂载 ISO 失败')];
    }
    return ['status' => 200, 'msg' => 'ISO 已挂载'];
}

function eyvescloud_isoDetach($params)
{
    $cid = eyvescloud_isoNumericID($params);
    if ($cid <= 0) {
        return ['status' => 'error', 'msg' => '无法解析容器编号，请用容器名称产品'];
    }
    $res = eyvescloud_request($params, '/api/isos/attach', ['container_id' => $cid, 'iso_id' => '', 'attach' => false], 'POST', 60);
    if (!eyvescloud_success($res)) {
        return ['status' => 'error', 'msg' => eyvescloud_message($res, '卸载 ISO 失败')];
    }
    return ['status' => 200, 'msg' => 'ISO 已卸载'];
}

// ---- 重装选系统：客户区 ---- 
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
            'name' => $t['name'] ?? $t['id'] ?? '',
            'arch' => $t['arch'] ?? '',
        ];
    }
    return ['status' => 200, 'msg' => '获取成功', 'data' => ['templates' => $list]];
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
    return ['status' => 200, 'msg' => '重装任务已提交'];
}

function eyvescloud_AllowFunction()
{
    $fns = ['TrafficReset', 'randomPort', 'addNat', 'updateNat', 'deleteNat', 'natList', 'infoData', 'webssh', 'vnc', 'firewallList', 'firewallUpdate',
        'snapshotList', 'snapshotCreate', 'snapshotRestore', 'snapshotDelete',
        'backupList', 'backupCreate', 'backupRestore', 'backupDelete',
        'isoList', 'isoAttach', 'isoDetach',
        'reinstallTemplates', 'reinstall'];
    return ['client' => $fns, 'admin' => $fns];
}

function eyvescloud_ClientArea($params)
{
    $tabs = [
        'info'     => ['name' => '实例信息'],
        'nat'      => ['name' => 'NAT转发'],
        'firewall' => ['name' => '防火墙'],
        'snapshot' => ['name' => '快照'],
        'backup'   => ['name' => '备份'],
        'reinstall'=> ['name' => '重装系统'],
    ];
    // ISO 挂载仅对 KVM 产品展示。
    $isKVM = strtolower((string)($params['configoptions']['virtualization'] ?? '')) === 'kvm';
    if ($isKVM) {
        $tabs['iso'] = ['name' => 'ISO挂载'];
    }
    return $tabs;
}

function eyvescloud_ClientAreaOutput($params, $key)
{
    $func = strtolower(trim((string)eyvescloud_request_value('func', '')));
    if ($func === 'natajax') {
        eyvescloud_json_response(eyvescloud_nat_ajax($params));
    }
    if ($func === 'infoajax') {
        eyvescloud_json_response(eyvescloud_info_ajax($params));
    }
    if ($func === 'firewallajax') {
        eyvescloud_json_response(eyvescloud_firewall_ajax($params));
    }

    // 通用客户端功能分发：func 对应 eyvescloud_<func>() 并返回 JSON。
    $clientFuncs = [
        'TrafficReset', 'randomPort', 'addNat', 'updateNat', 'deleteNat', 'natList',
        'infoData', 'webssh', 'vnc', 'firewallList', 'firewallUpdate',
        'snapshotList', 'snapshotCreate', 'snapshotRestore', 'snapshotDelete',
        'backupList', 'backupCreate', 'backupRestore', 'backupDelete',
        'isoList', 'isoAttach', 'isoDetach', 'reinstallTemplates', 'reinstall',
    ];
    if ($func !== '' && in_array($func, $clientFuncs, true)) {
        $fn = ($func === 'reinstall') ? 'eyvescloud_client_reinstall' : ('eyvescloud_' . $func);
        if (function_exists($fn)) {
            eyvescloud_json_response(call_user_func($fn, $params));
        }
    }

    if (!in_array($key, ['info', 'nat', 'firewall', 'snapshot', 'backup', 'iso', 'reinstall'], true)) {
        return '';
    }

    $res = eyvescloud_find_container($params);
    if (!eyvescloud_success($res) || empty($res['data'])) {
        return '获取实例信息失败: ' . eyvescloud_message($res, '未知错误');
    }

    $c = $res['data'];
    $publicHost = eyvescloud_public_host($params, $c, true);

    if ($key === 'nat') {
        $operation = eyvescloud_handle_nat_post($params);
        $operationMsg = $operation['message'] ?? '';
        $postMappings = $operation['mappings'] ?? null;
        if ($operationMsg !== '') {
            $res = eyvescloud_find_container($params);
            $c = eyvescloud_success($res) && !empty($res['data']) && is_array($res['data']) ? $res['data'] : $c;
        }
        $mappings = $postMappings !== null ? $postMappings : eyvescloud_port_mappings_from_container($c);

        return [
            'template' => 'templates/nat.html',
            'vars'     => [
                'container'     => $c,
                'container_name'=> $c['name'] ?? eyvescloud_container_name($params),
                'ssh_port'      => $c['ssh_port'] ?? '',
                'server_ip'     => $publicHost,
                'nat_host'      => $publicHost,
                'operation_msg' => $operationMsg,
                'service_id'    => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'      => 'nat',
                'port_mappings' => eyvescloud_normalize_port_mappings($mappings),
            ],
        ];
    }

    if ($key === 'firewall') {
        return [
            'template' => 'templates/firewall.html',
            'vars'     => [
                'container'      => $c,
                'container_name' => $c['name'] ?? eyvescloud_container_name($params),
                'server_ip'      => $publicHost,
                'service_id'     => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'       => 'firewall',
            ],
        ];
    }

    $currentTemplate = $c['template'] ?? ($params['configoptions']['template_id'] ?? '');

    if ($key === 'snapshot') {
        return [
            'template' => 'templates/snapshot.html',
            'vars'     => [
                'container'      => $c,
                'container_name' => $c['name'] ?? eyvescloud_container_name($params),
                'server_ip'      => $publicHost,
                'service_id'     => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'       => 'snapshot',
            ],
        ];
    }

    if ($key === 'backup') {
        return [
            'template' => 'templates/backup.html',
            'vars'     => [
                'container'      => $c,
                'container_name' => $c['name'] ?? eyvescloud_container_name($params),
                'server_ip'      => $publicHost,
                'service_id'     => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'       => 'backup',
            ],
        ];
    }

    if ($key === 'iso') {
        return [
            'template' => 'templates/iso.html',
            'vars'     => [
                'container'      => $c,
                'container_name' => $c['name'] ?? eyvescloud_container_name($params),
                'server_ip'      => $publicHost,
                'container_id'   => eyvescloud_isoNumericID($params),
                'service_id'     => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'       => 'iso',
            ],
        ];
    }

    if ($key === 'reinstall') {
        return [
            'template' => 'templates/reinstall.html',
            'vars'     => [
                'container'       => $c,
                'container_name'  => $c['name'] ?? eyvescloud_container_name($params),
                'server_ip'       => $publicHost,
                'current_template'=> $currentTemplate,
                'service_id'      => eyvescloud_request_value('id', $params['hostid'] ?? ''),
                'area_key'        => 'reinstall',
            ],
        ];
    }

    $initialRxBytes = (int)($c['traffic_used_rx'] ?? $c['rx_bytes'] ?? 0);
    $initialTxBytes = (int)($c['traffic_used_tx'] ?? $c['tx_bytes'] ?? 0);
    $initialTrafficUsed = ($initialRxBytes || $initialTxBytes) ? round(($initialRxBytes + $initialTxBytes) / 1073741824, 2) : '-';
    $initialTrafficIn = $initialRxBytes ? round($initialRxBytes / 1073741824, 2) : '-';
    $initialTrafficOut = $initialTxBytes ? round($initialTxBytes / 1073741824, 2) : '-';
    $initialTrafficLimit = isset($c['monthly_traffic_gb']) && $c['monthly_traffic_gb'] !== '' ? $c['monthly_traffic_gb'] : '-';
    $initialTrafficUsedText = ($initialRxBytes || $initialTxBytes) ? eyvescloud_format_bytes($initialRxBytes + $initialTxBytes) : '-';
    $initialTrafficInText = $initialRxBytes ? eyvescloud_format_bytes($initialRxBytes) : '-';
    $initialTrafficOutText = $initialTxBytes ? eyvescloud_format_bytes($initialTxBytes) : '-';
    $initialTrafficLimitText = is_numeric($initialTrafficLimit) ? round((float)$initialTrafficLimit, 2) . ' GB' : '-';
    $options = $params['configoptions'] ?? [];

    return [
        'template' => 'templates/info.html',
        'vars'     => [
            'container'      => $c,
            'status_text'    => (($c['status'] ?? '') === 'running') ? '运行中' : '已关机',
            'server_ip'      => $publicHost,
            'ssh_host'       => $publicHost,
            'ssh_port'       => $c['ssh_port'] ?? '',
            'ssh_password'   => $c['ssh_password'] ?? '',
            'ipv4'           => $c['ip'] ?? '',
            'ipv6'           => $c['ipv6'] ?? '',
            'vcpu'           => $c['vcpu'] ?? ($options['vcpu'] ?? ''),
            'ram_mb'         => $c['ram_mb'] ?? ($options['ram_mb'] ?? ''),
            'disk_gb'        => $c['disk_gb'] ?? ($options['disk_gb'] ?? ''),
            'bandwidth'      => $c['network_bw_mbps'] ?? ($options['network_bw_mbps'] ?? ''),
            'traffic_used'   => $initialTrafficUsed,
            'traffic_limit'  => $initialTrafficLimit,
            'traffic_in_gb'  => $initialTrafficIn,
            'traffic_out_gb' => $initialTrafficOut,
            'traffic_used_text' => $initialTrafficUsedText,
            'traffic_limit_text'=> $initialTrafficLimitText,
            'traffic_in_text'   => $initialTrafficInText,
            'traffic_out_text'  => $initialTrafficOutText,
            'expires_at'     => $c['expires_at'] ?? '',
            'service_id'     => eyvescloud_request_value('id', $params['hostid'] ?? ''),
            'area_key'       => 'info',
        ],
    ];
}
