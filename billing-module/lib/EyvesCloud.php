<?php
/**
 * EyvesCloud 面板 API 传输层（WHMCS 模块适配器）
 *
 * 设计目标：把魔方(ZJMF)风格 WHMCS 模块改造成对接 EyvesCloud 面板。
 * 本类只负责「连接 + 认证 + 请求 + 运行时(LXC/KVM)语义收敛」，
 * 不掺任何模板/展示逻辑（展示映射见 EyvesMapper）。
 *
 * 运行时矩阵（唯一权威定义，禁止在模板里再判断一次）：
 *   ┌──────────┬───────┬───────┐
 *   │ 能力      │ LXC   │ KVM   │
 *   ├──────────┼───────┼───────┤
 *   │ 镜像      │ 有    │ 有    │  ← GET /images?runtime=xxx 严格过滤
 *   │ SSH 控制台│ 有    │ 有    │
 *   │ VNC 控制台│ 无    │ 有    │  ← LXC 请求 vnc 会被后端 400 拒绝
 *   │ ISO 挂载  │ 无    │ 有    │
 *   │ 救援模式  │ 无    │ 有    │
 *   │ 数据盘    │ 有    │ 有    │  ← 均为单块 data_disk_gb（非魔方式多盘）
 *   │ 云初始化  │ 有    │ 有    │
 *   └──────────┴───────┴───────┘
 */


namespace WHMCS\Module\Server\EyvesCloud;

class EyvesCloudException extends \RuntimeException
{
    /** @var string */
    public $apiCode = '';
    /** @var int */
    public $httpStatus = 0;

    public function __construct($message, $apiCode = '', $httpStatus = 0)
    {
        parent::__construct($message);
        $this->apiCode   = $apiCode;
        $this->httpStatus = $httpStatus;
    }
}

class EyvesCloud
{
    /**
     * array_is_list 是 PHP 8.1+；模块要兼容 7.4/8.0，所以自己实现一份。
     * v1 的多数列表端点直接返回裸数组，需要用它来区分「裸数组」与「{items:[]}」。
     */
    protected static function isList(array $a)
    {
        if (function_exists('array_is_list')) {
            return array_is_list($a);
        }
        $i = 0;
        foreach ($a as $k => $_) {
            if ($k !== $i++) {
                return false;
            }
        }
        return true;
    }

    const RUNTIME_LXC = 'lxc';
    const RUNTIME_KVM = 'kvm';

    /** @var string http(s)://host:port */
    protected $base;
    /** @var string */
    protected $username;
    /** @var string */
    protected $password;
    /** @var string 预置 API Key（优先于账号密码登录） */
    protected $apiKey = '';
    /** @var string */
    protected $token = '';
    /** @var int */
    protected $tokenExpireAt = 0;
    /** @var int */
    protected $timeout = 30;
    /** @var bool */
    protected $verifySSL = false;
    /** @var array 最近一次响应包（调试/排错用） */
    public $lastEnvelope = [];
    /** @var array 请求日志（调试） */
    public $debugLog = [];
    /** @var bool */
    public $debug = false;

    /**
     * @param array $params WHMCS 服务器模块参数（含 serverid）
     * @param array $serverRow 可选：直接传入 tblservers 行（脱离 WHMCS 单测用）
     */
    public function __construct(array $params = [], array $serverRow = null)
    {
        if ($serverRow === null) {
            $serverRow = self::loadServerRow(
                isset($params['serverid']) ? (int) $params['serverid'] : 0
            );
        }
        if (empty($serverRow)) {
            throw new EyvesCloudException('无法读取 WHMCS 服务器配置（tblservers）');
        }

        $host = '';
        if (!empty($serverRow['hostname'])) {
            $host = $serverRow['hostname'];
        } elseif (!empty($serverRow['ipaddress'])) {
            $host = $serverRow['ipaddress'];
        }
        if ($host === '') {
            throw new EyvesCloudException('服务器地址为空');
        }

        $scheme = !empty($serverRow['secure']) ? 'https' : 'http';
        $port   = !empty($serverRow['port']) ? (int) $serverRow['port'] : (!empty($serverRow['secure']) ? 443 : 80);

        // 支持 [v6]:port 形式
        if (strpos($host, ':') !== false && strpos($host, '[') === false) {
            $host = '[' . $host . ']';
        }
        $this->base = $scheme . '://' . $host . ':' . $port;

        $this->username = isset($serverRow['username']) ? (string) $serverRow['username'] : 'admin';
        $this->password = self::decryptServerSecret(
            isset($serverRow['password']) ? (string) $serverRow['password'] : ''
        );

        // accesshash 里可以放 JSON：{"api_key":"...","verify_ssl":true,"timeout":60}
        $extra = self::parseAccessHash(isset($serverRow['accesshash']) ? (string) $serverRow['accesshash'] : '');
        if (!empty($extra['api_key'])) {
            $this->apiKey = (string) $extra['api_key'];
        }
        if (isset($extra['verify_ssl'])) {
            $this->verifySSL = (bool) $extra['verify_ssl'];
        }
        if (!empty($extra['timeout'])) {
            $this->timeout = (int) $extra['timeout'];
        }
        $this->debug = !empty($extra['debug']);
    }

    /* ---------------------------------------------------------------------
     * 构造辅助（可在非 WHMCS 环境覆盖）
     * ------------------------------------------------------------------- */

    protected static function loadServerRow($serverid)
    {
        if (!class_exists('\WHMCS\Database\Capsule')) {
            return [];
        }
        $row = \WHMCS\Database\Capsule::table('tblservers')->where('id', $serverid)->first();
        return $row ? (array) $row : [];
    }

    protected static function decryptServerSecret($value)
    {
        if ($value === '') {
            return '';
        }
        // WHMCS 后台保存的密码是加密的；脱离 WHMCS 单测时按明文处理。
        if (function_exists('decrypt')) {
            try {
                $plain = decrypt($value);
                if (is_string($plain) && $plain !== '') {
                    return $plain;
                }
            } catch (\Throwable $e) {
                // 落回明文
            }
        }
        return $value;
    }

    protected static function parseAccessHash($raw)
    {
        $raw = htmlspecialchars_decode((string) $raw, ENT_QUOTES);
        if ($raw === '') {
            return [];
        }
        // 兼容魔方遗留格式：zjmfdir=<x>|xxx，解析不出 JSON 就忽略
        $decoded = json_decode($raw, true);
        if (is_array($decoded)) {
            return $decoded;
        }
        $out = [];
        foreach (preg_split('/[|&,]/', $raw) as $pair) {
            if (strpos($pair, '=') === false) {
                continue;
            }
            list($k, $v) = explode('=', $pair, 2);
            $out[trim($k)] = trim($v);
        }
        return $out;
    }

    /* ---------------------------------------------------------------------
     * 认证
     * ------------------------------------------------------------------- */

    public function baseUrl()
    {
        return $this->base;
    }

    /**
     * 运行时字段名兼容：v1 是 virtualization，v2 是 runtime。
     * 只认一个的话，另一种版本的实例会被一律判成 LXC，
     * 结果是 KVM 的 VNC / ISO / 救援入口整片消失。
     */
    public function runtimeOf(array $instance)
    {
        $rt = '';
        foreach (['virtualization', 'runtime', 'type'] as $k) {
            if (!empty($instance[$k])) { $rt = strtolower(trim((string) $instance[$k])); break; }
        }
        return $rt === self::RUNTIME_KVM ? self::RUNTIME_KVM : self::RUNTIME_LXC;
    }

    public function isKVMInstance(array $instance)
    {
        return $this->runtimeOf($instance) === self::RUNTIME_KVM;
    }

    /**
     * 该实例支持的控台类型。这是 LXC/KVM 差异的**唯一**判定点。
     */
    public function consoleType(array $instance)
    {
        return $this->isKVMInstance($instance) ? 'vnc' : 'ssh';
    }

    public function supportsVNC(array $instance)
    {
        return $this->isKVMInstance($instance);
    }

    public function supportsISO(array $instance)
    {
        return $this->isKVMInstance($instance);
    }

    public function supportsRescue(array $instance)
    {
        return $this->isKVMInstance($instance);
    }

    /** 登录换 token（无 apiKey 时） */
    public function login($force = false)
    {
        if ($this->apiKey !== '') {
            return $this->apiKey;
        }
        if (!$force && $this->token !== '' && time() < $this->tokenExpireAt - 60) {
            return $this->token;
        }
        // v1 登录端点：POST /api/login {username,password} -> {data:{token}}
        $env = $this->raw('POST', '/api/login', [
            'username' => $this->username,
            'password' => $this->password,
        ], [], false);

        if (empty($env['ok'])) {
            throw new EyvesCloudException(
                isset($env['message']) ? $env['message'] : '面板登录失败',
                isset($env['code']) ? $env['code'] : '',
                isset($env['status']) ? $env['status'] : 0
            );
        }
        $data = isset($env['data']) ? $env['data'] : [];
        // v1 叫 token，v2 叫 access_token —— 两个都认，便于将来切换
        $this->token = '';
        foreach (['token', 'access_token'] as $k) {
            if (!empty($data[$k])) { $this->token = (string) $data[$k]; break; }
        }
        $ttl = isset($data['expires_in']) ? (int) $data['expires_in'] : 3600;
        $this->tokenExpireAt = time() + max(120, $ttl);
        if ($this->token === '') {
            throw new EyvesCloudException('面板未返回 access_token');
        }
        return $this->token;
    }

    /* ---------------------------------------------------------------------
     * 底层 HTTP
     * ------------------------------------------------------------------- */

    /**
     * 原始请求：返回 envelope ['ok'=>bool,'status'=>int,'code'=>str,'message'=>str,'data'=>mixed,'raw'=>str]
     */
    public function raw($method, $path, array $body = null, array $query = [], $auth = true)
    {
        $url = $this->base . $path;
        if (!empty($query)) {
            $url .= (strpos($url, '?') === false ? '?' : '&') . http_build_query($query);
        }

        $headers = ['Accept: application/json'];
        if ($auth) {
            $headers[] = 'Authorization: Bearer ' . $this->login();
        }
        $payload = null;
        if ($body !== null) {
            $payload = json_encode($body, JSON_UNESCAPED_UNICODE);
            $headers[] = 'Content-Type: application/json';
        }

        $ch = curl_init($url);
        curl_setopt_array($ch, [
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CUSTOMREQUEST  => strtoupper($method),
            CURLOPT_HTTPHEADER     => $headers,
            CURLOPT_TIMEOUT        => $this->timeout,
            CURLOPT_CONNECTTIMEOUT => 10,
            CURLOPT_SSL_VERIFYPEER => $this->verifySSL,
            CURLOPT_SSL_VERIFYHOST => $this->verifySSL ? 2 : 0,
        ]);
        if ($payload !== null) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, $payload);
        }
        $resp   = curl_exec($ch);
        $errNo  = curl_errno($ch);
        $errMsg = curl_error($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_HTTP_CODE);
        curl_close($ch);

        if ($this->debug) {
            $this->debugLog[] = strtoupper($method) . ' ' . $path . ' -> ' . $status;
        }

        $env = [
            'ok' => false, 'status' => $status, 'code' => '', 'message' => '',
            'data' => null, 'raw' => (string) $resp,
        ];
        if ($errNo !== 0) {
            $env['code']    = 'CURL_ERROR';
            $env['message'] = '无法连接面板：' . $errMsg;
            $this->lastEnvelope = $env;
            return $env;
        }

        $json = json_decode((string) $resp, true);
        if (!is_array($json)) {
            $env['code']    = 'BAD_RESPONSE';
            $env['message'] = '面板返回非 JSON（HTTP ' . $status . '）';
            $this->lastEnvelope = $env;
            return $env;
        }

        // eyvescloud 统一信封：{success, code, message, data, request_id}
        $env['code']    = isset($json['code']) ? (string) $json['code'] : '';
        $env['message'] = isset($json['message']) ? (string) $json['message'] : '';
        $env['data']    = isset($json['data']) ? $json['data'] : null;
        $env['ok']      = !empty($json['success']) || (isset($json['data']) && $status >= 200 && $status < 300);

        // 瞬时故障重试一次：连接错误或 5xx。只对幂等的 GET 生效，
        // 避免写操作被重复执行；重试前短暂退避。
        // 面板偶尔抖动不应该让整个客户区变成错误页。
        $transient = ($env['code'] === 'CURL_ERROR') || ($status >= 500 && $status < 600);
        if (!$env['ok'] && $transient && strtoupper($method) === 'GET'
            && !$this->retryGuard && !$this->reloginGuard) {
            $this->retryGuard = true;
            usleep(300000); // 0.3s
            $again = $this->raw($method, $path, $body, $query, $auth);
            $this->retryGuard = false;
            return $again;
        }

        // token 失效自动重登一次（仅登录态、非 apiKey、且不是登录请求本身）
        if (!$env['ok'] && $status === 401 && $auth && $this->apiKey === '' && !$this->reloginGuard) {
            $this->reloginGuard = true;
            try {
                $this->login(true);
                $this->reloginGuard = false;
                return $this->raw($method, $path, $body, $query, $auth);
            } catch (\Throwable $e) {
                $this->reloginGuard = false;
            }
        }
        $this->lastEnvelope = $env;
        return $env;
    }

    /** @var bool 防止 401 重登递归 */
    protected $reloginGuard = false;
    /** @var bool 防止瞬时故障重试递归 */
    protected $retryGuard = false;

    /**
     * 便捷请求：成功返回 data，失败抛异常（写操作用）
     */
    public function call($method, $path, array $body = null, array $query = [])
    {
        $env = $this->raw($method, $path, $body, $query);
        if (!$env['ok']) {
            throw new EyvesCloudException(
                $env['message'] !== '' ? $env['message'] : ('面板请求失败：' . $method . ' ' . $path),
                $env['code'],
                $env['status']
            );
        }
        return $env['data'];
    }

    /**
     * 只读请求：失败返回 null 而不是抛错（列表类，模板渲染要稳）
     */
    public function tryCall($method, $path, array $body = null, array $query = [])
    {
        try {
            return $this->call($method, $path, $body, $query);
        } catch (\Throwable $e) {
            return null;
        }
    }

    /* ---------------------------------------------------------------------
     * 连接自检
     * ------------------------------------------------------------------- */

    public function testConnection()
    {
        try {
            $data = $this->call('GET', '/api/health');
        } catch (EyvesCloudException $e) {
            return ['success' => false, 'error' => $e->getMessage()];
        }
        $version = '';
        foreach (['version', 'panel_version', 'build'] as $k) {
            if (!empty($data[$k])) {
                $version = (string) $data[$k];
                break;
            }
        }
        return ['success' => true, 'version' => $version, 'raw' => $data];
    }

    /* ---------------------------------------------------------------------
     * 目录类（区域 / 节点 / 镜像 / 存储池 / IP 池）
     * ------------------------------------------------------------------- */

    /** 拉全量列表（自动翻页） */
    public function all($path, array $query = [], $pageSize = 100, $maxPages = 20)
    {
        $items = [];
        $page  = 1;
        while ($page <= $maxPages) {
            $q = $query;
            $q['page'] = $page;
            $q['page_size'] = $pageSize;
            $data = $this->tryCall('GET', $path, null, $q);
            if ($data === null) {
                break;
            }
            // v1 多数列表直接返回裸数组，v2 返回 {items, pagination}，两种都要认
            if (isset($data['items']) && is_array($data['items'])) {
                $batch = $data['items'];
            } elseif (is_array($data) && self::isList($data)) {
                $batch = $data;
            } else {
                $batch = [];
            }
            $items = array_merge($items, $batch);
            $pg = isset($data['pagination']) ? $data['pagination'] : [];
            $pages = isset($pg['pages']) ? (int) $pg['pages'] : 1;
            // 裸数组没有分页信息：少于整页就说明取完了
            if ($page >= $pages || empty($batch) || count($batch) < $pageSize) {
                break;
            }
            $page++;
        }
        return $items;
    }

    /**
     * 镜像列表。$runtime = lxc|kvm|null(全部)。
     * 必须按 runtime 过滤，否则会把 LXC 模板塞进 KVM 产品的重装下拉里。
     */
    public function images($runtime = null, $onlyEnabled = true)
    {
        // v1 的 /api/images 不支持按运行时过滤（type 参数会被忽略），
        // 一律取全量再在本地过滤 —— 比依赖一个不生效的服务端参数可靠。
        $items = $this->all('/api/images');
        if ($runtime === self::RUNTIME_LXC || $runtime === self::RUNTIME_KVM) {
            $items = array_values(array_filter($items, function ($it) use ($runtime) {
                $t = isset($it['type']) ? strtolower((string) $it['type'])
                     : (isset($it['runtime']) ? strtolower((string) $it['runtime']) : '');
                return $t === $runtime;
            }));
        }
        if ($onlyEnabled) {
            $items = array_values(array_filter($items, function ($it) {
                return !isset($it['enabled']) || !empty($it['enabled']);
            }));
        }
        // 统一补出 runtime 字段：v1 用 type、v2 用 runtime，
        // 归一放在传输层，下游（映射层/模板/测试）只见一种写法。
        foreach ($items as &$it) {
            if (!isset($it['runtime'])) {
                $it['runtime'] = isset($it['type']) ? strtolower((string) $it['type']) : 'lxc';
            }
        }
        unset($it);
        return $items;
    }

    public function imagesGrouped()
    {
        $out = [self::RUNTIME_LXC => [], self::RUNTIME_KVM => []];
        foreach ($this->images(null, false) as $it) {
            $rt = isset($it['type']) ? strtolower((string) $it['type'])
                : strtolower((string) (isset($it['runtime']) ? $it['runtime'] : self::RUNTIME_LXC));
            if (!isset($out[$rt])) {
                $out[$rt] = [];
            }
            $out[$rt][] = $it;
        }
        return $out;
    }

    public function regions()
    {
        return $this->all('/api/regions');
    }

    public function nodes($regionId = '')
    {
        $q = [];
        if ($regionId !== '') {
            $q['region_id'] = $regionId;
        }
        return $this->all('/api/nodes', $q);
    }

    public function ipPools()
    {
        return $this->all('/api/ip-groups');
    }

    public function storagePools($runtime = null)
    {
        $items = $this->all('/api/storage');
        if ($runtime === null) {
            return $items;
        }
        return array_values(array_filter($items, function ($p) use ($runtime) {
            $types = isset($p['content_types']) && is_array($p['content_types']) ? $p['content_types'] : [];
            return empty($types) || in_array($runtime, $types, true);
        }));
    }

    public function isoImages()
    {
        return $this->all('/api/isos');
    }

    public function sshKeys()
    {
        return $this->all('/api/ssh-keys');
    }

    public function securityGroups()
    {
        return $this->all('/api/security-groups');
    }

    /* ---------------------------------------------------------------------
     * 实例 CRUD
     * ------------------------------------------------------------------- */

    public function instances(array $filters = [])
    {
        $q = [];
        foreach (['status', 'node_id', 'region_id', 'search', 'tenant', 'owner'] as $k) {
            if (!empty($filters[$k])) {
                $q[$k] = $filters[$k];
            }
        }
        return $this->all('/api/containers', $q);
    }

    public function instance($id)
    {
        return $this->call('GET', '/api/containers/' . rawurlencode($id));
    }

    public function findInstanceByName($name)
    {
        foreach ($this->instances() as $it) {
            if (isset($it['name']) && $it['name'] === $name) {
                return $it;
            }
        }
        return null;
    }

    /**
     * 创建实例。$spec 支持 runtime/name/template_id/vcpu/memory_mb/disk_gb/
     * data_disk_gb/node_id/auth/ssh_port/nat_ports/assign_nat/public_ipv4_count/
     * ipv6_count/storage_pool_id/cloud_init/expires_at/tenant/remark 等。
     *
     * LXC/KVM 差异处理：
     *   - runtime 缺省为 lxc
     *   - template_id 必须与该 runtime 匹配的镜像 ID（调用方保证，见 specFromProduct）
     */
    public function createInstance(array $spec)
    {
        if (empty($spec['name']) || empty($spec['template_id'])) {
            throw new EyvesCloudException('创建实例缺少 name 或 template_id');
        }
        $spec['runtime'] = $this->normalizeRuntime(isset($spec['runtime']) ? $spec['runtime'] : self::RUNTIME_LXC);
        return $this->call('POST', '/api/containers', $spec);
    }

    public function normalizeRuntime($runtime)
    {
        return strtolower(trim((string) $runtime)) === self::RUNTIME_KVM ? self::RUNTIME_KVM : self::RUNTIME_LXC;
    }

    /**
     * 更新实例。v1 没有通用 PATCH，必须按字段分发到各自的动作端点。
     * 返回 ['applied' => [...], 'unsupported' => [...]]，让调用方知道哪些没生效 ——
     * 静默丢弃字段是最难查的那类问题。
     */
    public function updateInstance($id, array $patch)
    {
        $base    = '/api/containers/' . rawurlencode($id);
        $applied = [];
        $unsupported = [];

        // 计算资源：vCPU / 内存
        // v1 的字段名与 v2 不同：内存是 ram_mb、带宽是 network_down/up_mbps
        // applied 里回报的是**调用方传入的字段名**（不是 v1 的内部字段名），
        // 否则调用方无法把自己请求的字段和结果对应起来。
        $res = [];
        if (isset($patch['vcpu']))      { $res['vcpu'] = (float) $patch['vcpu']; $applied[] = 'vcpu'; }
        if (isset($patch['memory_mb'])) { $res['ram_mb'] = (int) $patch['memory_mb']; $applied[] = 'memory_mb'; }
        if (!empty($res)) {
            $this->call('PUT', $base . '/resource-limit', $res);
        }

        // 磁盘：v1 走 resize
        if (isset($patch['disk_gb'])) {
            $this->call('POST', $base . '/resize', ['disk_gb' => (float) $patch['disk_gb']]);
            $applied[] = 'disk_gb';
        }
        if (isset($patch['data_disk_gb'])) {
            $this->call('POST', $base . '/resize', ['data_disk_gb' => (float) $patch['data_disk_gb']]);
            $applied[] = 'data_disk_gb';
        }

        // 带宽
        $bw = [];
        if (isset($patch['down_mbps'])) { $bw['network_down_mbps'] = (int) $patch['down_mbps']; }
        if (isset($patch['up_mbps']))   { $bw['network_up_mbps']   = (int) $patch['up_mbps']; }
        if (!empty($bw)) {
            // v1 的 bandwidth 动作是 GET（流量明细），设置带宽走 resource-limit
            $this->call('PUT', $base . '/resource-limit', $bw);
            if (isset($patch['down_mbps'])) { $applied[] = 'down_mbps'; }
            if (isset($patch['up_mbps']))   { $applied[] = 'up_mbps'; }
        }

        // 流量配额
        if (isset($patch['traffic_quota_gb'])) {
            $this->call('PUT', $base . '/traffic-limit', ['monthly_traffic_gb' => (int) $patch['traffic_quota_gb']]);
            $applied[] = 'traffic_quota_gb';
        }

        // 到期时间
        if (isset($patch['expires_at'])) {
            $this->call('PUT', $base . '/expiry', ['expires_at' => (string) $patch['expires_at']]);
            $applied[] = 'expires_at';
        }

        // v1 没有对应动作的字段，明确报出来而不是静默丢弃
        foreach (array_keys($patch) as $k) {
            if (!in_array($k, $applied, true)) { $unsupported[] = $k; }
        }
        return ['applied' => $applied, 'unsupported' => $unsupported];
    }

    public function deleteInstance($id, $purge = false)
    {
        $path = '/api/containers/' . rawurlencode($id);
        $q = $purge ? ['purge' => 'true'] : [];
        return $this->call('DELETE', $path, null, $q);
    }

    public function purgeInstance($id)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/purge');
    }

    public function restoreFromRecycleBin($id)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/restore');
    }

    /**
     * 电源操作。action ∈ start|stop|shutdown|restart|hard-stop|hard-restart|suspend|unsuspend
     * 后端已为 LXC/KVM 统一语义，此处不做分支。
     */
    public function power($id, $action)
    {
        $map = [
            'start' => 'start', 'stop' => 'stop', 'shutdown' => 'shutdown',
            'restart' => 'restart', 'reboot' => 'restart',
            'hard-stop' => 'hard-stop', 'hardoff' => 'hard-stop', 'hardstop' => 'hard-stop',
            'hard-restart' => 'hard-restart', 'hardreboot' => 'hard-restart',
            'suspend' => 'suspend', 'unsuspend' => 'unsuspend',
        ];
        $a = strtolower(trim((string) $action));
        if (!isset($map[$a])) {
            throw new EyvesCloudException('不支持的电源操作：' . $action);
        }
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/' . $map[$a]);
    }

    /** 重装系统。$imageId 必须是该实例 runtime 下的镜像 ID */
    public function reinstall($id, $imageId, $authMode = '', $password = '', array $sshKeyIds = [])
    {
        $body = ['image_id' => $imageId];
        if ($authMode !== '') {
            $body['auth_mode'] = $authMode;
        }
        if ($password !== '') {
            $body['password'] = $password;
        }
        if (!empty($sshKeyIds)) {
            $body['ssh_key_ids'] = $sshKeyIds;
        }
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/reinstall', $body);
    }

    /** 重置密码。KVM 走 qemu-guest-agent，LXC 走 chroot 改 /etc/shadow，API 层无差异 */
    public function resetPassword($id, $password = '')
    {
        $body = [];
        if ($password !== '') {
            $body['password'] = $password;
        }
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/reset-password', $body);
    }

    /**
     * 获取控制台票据。
     * LXC → ssh/webssh；KVM → vnc。$type 为空时按 runtime 自动选择。
     */
    public function console(array $instance, $type = '')
    {
        $type = strtolower(trim((string) $type));
        if ($type === '') {
            $type = $this->consoleType($instance);
        }
        // 兜底：LXC 请求 vnc 会 400，直接本地纠正并告知调用方
        if ($type === 'vnc' && !$this->supportsVNC($instance)) {
            throw new EyvesCloudException('LXC 实例不支持 VNC 控制台，请使用 SSH 终端');
        }
        // v1 用独立的票据端点，且**服务端本身就会校验运行时**
        // （对 LXC 申请 vnc 会返回 "VNC console is only available for KVM VMs"）。
        $path = $type === 'vnc' ? '/api/vnc-ticket' : '/api/ssh-ticket';
        $res = $this->call('POST', $path, ['container_name' => $instance['name']]);
        $ticket = isset($res['ticket']) ? (string) $res['ticket'] : '';
        if ($ticket === '') {
            throw new EyvesCloudException('面板未返回控制台票据');
        }
        // 票据需要换成 WebSocket 地址，前端才能连
        $wsScheme = (strpos($this->base, 'https://') === 0) ? 'wss' : 'ws';
        $host = preg_replace('#^https?://#', '', $this->base);
        if ($type === 'vnc') {
            $url = $wsScheme . '://' . $host . '/api/vnc?container=' . rawurlencode($instance['name'])
                 . '&ticket=' . rawurlencode($ticket);
        } else {
            $url = $wsScheme . '://' . $host . '/api/ssh?container=' . rawurlencode($instance['name']);
        }
        return [
            'type'         => $type,
            'ticket'       => $ticket,
            'url'          => $url,
            'subprotocol'  => $type === 'vnc' ? null : ('eyvescloud-ticket.' . $ticket),
            'container'    => $instance['name'],
            'expires_in'   => 60,
        ];
    }

    /** 网络配置（IP / 带宽 / 端口） */
    public function updateNetwork($id, array $net)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/bandwidth', $net);
    }

    public function metrics($id, $range = '1h')
    {
        return $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/usage');
    }

    public function usage($id)
    {
        return $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/usage');
    }

    public function events($id)
    {
        return $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/history');
    }

    /* ---------------------------------------------------------------------
     * 快照 / 备份
     * ------------------------------------------------------------------- */

    public function snapshots($id)
    {
        $data = $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/snapshots');
        if (is_array($data) && isset($data['items'])) {
            return $data['items'];
        }
        return is_array($data) ? $data : [];
    }

    public function createSnapshot($id, $name = '', $desc = '')
    {
        $body = [];
        if ($name !== '') {
            $body['name'] = $name;
        }
        if ($desc !== '') {
            $body['description'] = $desc;
        }
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/snapshots', $body);
    }

    public function restoreSnapshot($id, $sid)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/snapshots/' . rawurlencode($sid) . '/restore');
    }

    public function deleteSnapshot($id, $sid)
    {
        return $this->call('DELETE', '/api/containers/' . rawurlencode($id) . '/snapshots/' . rawurlencode($sid));
    }

    public function backups($id)
    {
        $data = $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/backups');
        if (is_array($data) && isset($data['items'])) {
            return $data['items'];
        }
        return is_array($data) ? $data : [];
    }

    public function createBackup($id)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/backups');
    }

    public function restoreBackup($id, $bid)
    {
        return $this->call('POST', '/api/containers/' . rawurlencode($id) . '/backups/' . rawurlencode($bid) . '/restore');
    }

    public function deleteBackup($id, $bid)
    {
        return $this->call('DELETE', '/api/containers/' . rawurlencode($id) . '/backups/' . rawurlencode($bid));
    }

    /* ---------------------------------------------------------------------
     * 安全组 / SSH 密钥 / 端口映射
     * ------------------------------------------------------------------- */

    /**
     * 容器防火墙。v1 的安全模型是「每个容器一组规则」，
     * 没有 v2 那种可复用的「安全组」资源 —— 这也正是它更简单可靠的地方。
     */
    public function instanceFirewall($id)
    {
        $data = $this->tryCall('GET', '/api/containers/' . rawurlencode($id) . '/firewall');
        if (!is_array($data)) {
            return ['enabled' => false, 'default_action' => '', 'rules' => []];
        }
        return [
            'enabled'        => !empty($data['enabled']),
            'default_action' => isset($data['default_action']) ? (string) $data['default_action'] : '',
            'rules'          => isset($data['rules']) && is_array($data['rules']) ? $data['rules'] : [],
        ];
    }

    public function setInstanceFirewall($id, array $rules, $enabled = true, $defaultAction = '')
    {
        $body = ['enabled' => (bool) $enabled, 'rules' => array_values($rules)];
        if ($defaultAction !== '') {
            $body['default_action'] = $defaultAction;
        }
        return $this->call('PUT', '/api/containers/' . rawurlencode($id) . '/firewall', $body);
    }

    /* 兼容旧命名（模块此前按 v2 的「安全组」建模）：统一走防火墙规则 */
    public function instanceSecurityGroups($id)
    {
        return $this->instanceFirewall($id)['rules'];
    }

    public function setInstanceSecurityGroups($id, array $groupIds)
    {
        return $this->setInstanceFirewall($id, $groupIds, true);
    }

    public function securityGroupRules($gid)
    {
        $data = $this->tryCall('GET', '/api/security-groups/' . rawurlencode($gid));
        if (is_array($data) && isset($data['rules']) && is_array($data['rules'])) {
            return $data['rules'];
        }
        return [];
    }

    public function createSecurityGroup($name, $desc = '')
    {
        return $this->call('POST', '/api/security-groups', ['name' => $name, 'description' => $desc]);
    }

    public function deleteSecurityGroup($gid)
    {
        return $this->call('DELETE', '/api/security-groups/' . rawurlencode($gid));
    }

    /**
     * 端口映射列表。
     *
     * 注意：v1 的 /containers/{id}/port-mappings 返回的是**整个容器对象**，
     * 映射列表在其 port_mappings 字段里；直接把它当列表用会得到一堆垃圾行。
     */
    public function portMappings($id)
    {
        $data = $this->tryCall('GET', '/api/v1/containers/' . rawurlencode($id) . '/port-mappings');
        if (!is_array($data)) {
            return [];
        }
        if (isset($data['port_mappings']) && is_array($data['port_mappings'])) {
            return $data['port_mappings'];
        }
        if (isset($data['items']) && is_array($data['items'])) {
            return $data['items'];
        }
        return [];
    }

    public function createPortMapping($id, array $map)
    {
        return $this->call('POST', '/api/v1/containers/' . rawurlencode($id) . '/port-mappings', $map);
    }

    public function deletePortMapping($id, $index)
    {
        return $this->call('DELETE', '/api/v1/containers/' . rawurlencode($id) . '/port-mappings/' . rawurlencode($index));
    }

    public function randomPort($id)
    {
        return $this->tryCall('GET', '/api/v1/containers/' . rawurlencode($id) . '/random-port');
    }

    /* ---------------------------------------------------------------------
     * 任务
     * ------------------------------------------------------------------- */

    public function tasks(array $filters = [])
    {
        return $this->all('/api/tasks', $filters);
    }

    public function task($id)
    {
        return $this->call('GET', '/api/tasks/' . rawurlencode($id));
    }

    public function cancelTask($id)
    {
        return $this->call('POST', '/api/tasks/' . rawurlencode($id) . '/cancel');
    }

    /** 轮询任务直到结束（创建/重装等异步操作用） */
    public function waitTask($taskId, $timeoutSec = 120, $intervalSec = 2)
    {
        $deadline = time() + max(5, (int) $timeoutSec);
        $last = null;
        while (time() < $deadline) {
            $last = $this->tryCall('GET', '/api/tasks/' . rawurlencode($taskId));
            if (is_array($last)) {
                $st = strtolower((string) (isset($last['status']) ? $last['status'] : ''));
                if (in_array($st, ['success', 'failed', 'error', 'cancelled', 'canceled'], true)) {
                    return $last;
                }
            }
            sleep(max(1, (int) $intervalSec));
        }
        return $last;
    }

    /* ---------------------------------------------------------------------
     * 指标 / 概览
     * ------------------------------------------------------------------- */

    public function metricsSummary()
    {
        return $this->tryCall('GET', '/api/dashboard');
    }

    public function systemInfo()
    {
        return $this->tryCall('GET', '/api/health');
    }

    /**
     * 按 runtime 生成创建规格（WHMCS 产品配置项 → eyvescloud 请求体）。
     * runtime 由产品配置決定：LXC 与 KVM 是两个产品/两个配置模板。
     */
    public function specFromProduct(array $product, array $overrides = [])
    {
        $runtime = $this->normalizeRuntime(isset($product['runtime']) ? $product['runtime'] : self::RUNTIME_LXC);
        $spec = [
            'runtime'     => $runtime,
            'name'        => isset($product['name']) ? (string) $product['name'] : '',
            'template_id' => isset($product['template_id']) ? (string) $product['template_id'] : '',
            'vcpu'        => isset($product['vcpu']) ? (float) $product['vcpu'] : 1,
            'memory_mb'   => isset($product['memory_mb']) ? (int) $product['memory_mb'] : 512,
            'disk_gb'     => isset($product['disk_gb']) ? (float) $product['disk_gb'] : 10,
        ];
        $opt = [
            'data_disk_gb'       => 'float',
            'down_mbps'          => 'int',
            'up_mbps'            => 'int',
            'traffic_quota_gb'   => 'int',
            'ssh_port'           => 'int',
            'nat_ports'          => 'int',
            'public_ipv4_count'  => 'int',
            'ipv6_count'         => 'int',
        ];
        foreach ($opt as $k => $type) {
            if (isset($product[$k]) && $product[$k] !== '') {
                $spec[$k] = $type === 'float' ? (float) $product[$k] : (int) $product[$k];
            }
        }
        foreach (['node_id', 'storage_pool_id', 'tenant', 'remark', 'cloud_init', 'expires_at', 'traffic_mode'] as $k) {
            if (!empty($product[$k])) {
                $spec[$k] = (string) $product[$k];
            }
        }
        if (isset($product['assign_nat'])) {
            $spec['assign_nat'] = (bool) $product['assign_nat'];
        }
        if (isset($product['node_priority'])) {
            $spec['node_priority'] = (int) $product['node_priority'];
        }
        if (isset($product['firewall_enabled'])) {
            $spec['firewall_enabled'] = (bool) $product['firewall_enabled'];
        }
        if (!empty($product['password']) || !empty($product['ssh_key_ids'])) {
            $spec['auth'] = [
                'mode'        => !empty($product['ssh_key_ids']) ? 'ssh_key' : 'password',
                'password'    => isset($product['password']) ? (string) $product['password'] : '',
                'ssh_key_ids' => isset($product['ssh_key_ids']) && is_array($product['ssh_key_ids']) ? array_values($product['ssh_key_ids']) : [],
            ];
        }
        foreach ($overrides as $k => $v) {
            if ($v !== null && $v !== '') {
                $spec[$k] = $v;
            }
        }
        return $spec;
    }
}
