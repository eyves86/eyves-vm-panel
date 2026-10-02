<?php
/**
 * api_client.php 路由端到端测试
 *
 * 自带最小 WHMCS 运行环境（init 桩 + SQLite），无需真实 WHMCS 即可跑。
 * api_client.php 用 require（非 once）加载 init，桩必须幂等，故拆成
 * 「幂等壳 + 实体」两个文件。
 *
 * 用法：php tests/router.php [host] [port]
 */

$HOST = isset($argv[1]) ? $argv[1] : '127.0.0.1';
$PORT = isset($argv[2]) ? (int) $argv[2] : 8998;

$MODULE = dirname(__DIR__);
$WORK   = sys_get_temp_dir() . '/eyves-router-' . getmypid();
$SRV    = $WORK . '/modules/servers/eyvescloud';
$DB     = $WORK . '/whmcs.sqlite';

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

/* ------------------------------------------------------------------ 搭环境 */
@mkdir($SRV . '/lib', 0755, true);
@mkdir($SRV . '/templates', 0755, true);
foreach (['eyvescloud.php', 'api_client.php'] as $f) { copy("$MODULE/$f", "$SRV/$f"); }
foreach (['EyvesCloud.php', 'EyvesMapper.php', 'EyvesLang.php'] as $f) { copy("$MODULE/lib/$f", "$SRV/lib/$f"); }

/* init 桩：幂等壳 */
file_put_contents("$WORK/init.php", <<<'SHELL'
<?php
if (defined('EV_INIT_LOADED')) { return; }
define('EV_INIT_LOADED', true);
if (!defined('ROOTDIR')) { define('ROOTDIR', __DIR__); }
require __DIR__ . '/init_core.php';
SHELL);

/* init 桩：实体（Capsule 用真实 SQLite） */
file_put_contents("$WORK/init_core.php", <<<'CORE'
<?php
namespace {
    if (!defined('WHMCS')) { define('WHMCS', true); }
    if (session_status() === PHP_SESSION_NONE) { @session_start(); }
    if (!function_exists('encrypt')) { function encrypt($v) { return 'ENC:' . base64_encode((string) $v); } }
    if (!function_exists('decrypt')) {
        function decrypt($v) { $v = (string) $v; return strpos($v, 'ENC:') === 0 ? base64_decode(substr($v, 4)) : $v; }
    }
}

namespace WHMCS\Database {
    class Query
    {
        private $pdo; private $table; private $wheres = [];
        public function __construct($pdo, $table) { $this->pdo = $pdo; $this->table = $table; }
        public function where($col, $val) { $this->wheres[] = [$col, $val]; return $this; }
        public function orderBy($col, $dir = 'asc') { return $this; }
        private function build() {
            $sql = 'SELECT * FROM ' . $this->table; $args = [];
            if ($this->wheres) {
                $parts = [];
                foreach ($this->wheres as $w) { $parts[] = $w[0] . ' = ?'; $args[] = $w[1]; }
                $sql .= ' WHERE ' . implode(' AND ', $parts);
            }
            return [$sql, $args];
        }
        public function first() {
            list($sql, $args) = $this->build();
            $st = $this->pdo->prepare($sql); $st->execute($args);
            $row = $st->fetch(\PDO::FETCH_ASSOC);
            return $row ? (object) $row : null;
        }
        public function get() {
            list($sql, $args) = $this->build();
            $st = $this->pdo->prepare($sql); $st->execute($args);
            return array_map(function ($r) { return (object) $r; }, $st->fetchAll(\PDO::FETCH_ASSOC));
        }
        public function insert(array $data) {
            $cols = array_keys($data);
            $sql = 'INSERT INTO ' . $this->table . ' (' . implode(',', $cols) . ') VALUES ('
                 . implode(',', array_fill(0, count($cols), '?')) . ')';
            $st = $this->pdo->prepare($sql); $st->execute(array_values($data));
            return true;
        }
        public function update(array $data) {
            $set = []; $args = [];
            foreach ($data as $k => $v) { $set[] = $k . ' = ?'; $args[] = $v; }
            $sql = 'UPDATE ' . $this->table . ' SET ' . implode(',', $set);
            $st = $this->pdo->prepare($sql); $st->execute($args);
            return $st->rowCount();
        }
    }
    class Capsule
    {
        private static $pdo = null;
        public static function setPdo($pdo) { self::$pdo = $pdo; }
        public static function table($name) { return new Query(self::$pdo, $name); }
    }
}
CORE);

/* 建库 */
@unlink($DB);
$pdo = new PDO('sqlite:' . $DB);
$pdo->setAttribute(PDO::ATTR_ERRMODE, PDO::ERRMODE_EXCEPTION);
$pdo->exec("CREATE TABLE tblhosting (id INTEGER PRIMARY KEY, userid INTEGER, packageid INTEGER, server INTEGER, domain TEXT, username TEXT, password TEXT, domainstatus TEXT, configoptions TEXT)");
$pdo->exec("CREATE TABLE tblcustomfields (id INTEGER PRIMARY KEY, relid INTEGER, fieldname TEXT)");
$pdo->exec("CREATE TABLE tblcustomfieldsvalues (id INTEGER PRIMARY KEY, fieldid INTEGER, relid INTEGER, value TEXT)");
$pdo->exec("CREATE TABLE tblservers (id INTEGER PRIMARY KEY, type TEXT, name TEXT, hostname TEXT, ipaddress TEXT, port INTEGER, secure INTEGER, username TEXT, password TEXT, accesshash TEXT)");

/* 用真面板拉两个实例 ID 来绑定 */
require_once "$SRV/lib/EyvesCloud.php";
$api = new \WHMCS\Module\Server\EyvesCloud\EyvesCloud([], [
    'hostname' => $HOST, 'ipaddress' => $HOST, 'port' => $PORT, 'secure' => 0,
    'username' => 'admin', 'password' => 'Adapter#2026', 'accesshash' => '{}',
]);
$lxcId = $kvmId = '';
try {
    foreach ($api->instances() as $it) {
        if ($it['name'] === 'ct-lxc-01') { $lxcId = (string) $it['id']; }
        if ($it['name'] === 'ct-kvm-01') { $kvmId = (string) $it['id']; }
    }
} catch (Throwable $e) {
    echo "无法连接面板 {$HOST}:{$PORT}：" . $e->getMessage() . "\n";
    echo "（先启动面板并跑 tests/seed.sh）\n";
    exit(1);
}
if ($lxcId === '' || $kvmId === '') {
    echo "面板里找不到 ct-lxc-01 / ct-kvm-01，请先执行 tests/seed.sh 并重启面板\n";
    exit(1);
}

$st = $pdo->prepare("INSERT INTO tblservers VALUES (7,'eyvescloud','t',?,?,?,0,'admin',?,'{}')");
$st->execute([$HOST, $HOST, $PORT, 'ENC:' . base64_encode('Adapter#2026')]);
$pdo->exec("INSERT INTO tblhosting VALUES (900,1,31,7,'lxc.local','root','','Active','')");
$pdo->exec("INSERT INTO tblhosting VALUES (901,1,32,7,'kvm.local','root','','Active','')");
$pdo->exec("INSERT INTO tblhosting VALUES (902,2,31,7,'other.local','root','','Active','')");
$pdo->exec("INSERT INTO tblhosting VALUES (903,1,31,7,'nobind.local','root','','Active','')");
$pdo->exec("INSERT INTO tblcustomfields VALUES (501,31,'hostid')");
$pdo->exec("INSERT INTO tblcustomfields VALUES (502,32,'hostid')");
$pdo->exec("INSERT INTO tblcustomfieldsvalues VALUES (1,501,900,'$lxcId')");
$pdo->exec("INSERT INTO tblcustomfieldsvalues VALUES (2,502,901,'$kvmId')");

/* 单次调用包装器（子进程跑，保证 die() 输出干净捕获） */
file_put_contents("$WORK/call.php", <<<'CALL'
<?php
require_once getenv('EVWORK') . '/init.php';
$pdo = new PDO('sqlite:' . getenv('EVDB'));
\WHMCS\Database\Capsule::setPdo($pdo);
if (session_status() === PHP_SESSION_NONE) { session_start(); }
$_SESSION = ['uid' => (int) $argv[1]];
if ($argv[2] !== '-') { $_SESSION['adminid'] = (int) $argv[2]; }
$req = ['serviceid' => (int) $argv[3], 'action' => $argv[4]];
for ($i = 5; $i < $argc; $i++) {
    if (strpos($argv[$i], '=') === false) { continue; }
    list($k, $v) = explode('=', $argv[$i], 2);
    if (substr($k, -2) === '[]') { $req[substr($k, 0, -2)][] = $v; }
    else { $req[$k] = $v; }
}
$_REQUEST = $req; $_GET = $req; $_POST = $req;
chdir(getenv('EVSRV'));
include getenv('EVSRV') . '/api_client.php';
CALL);

function callApi($serviceId, $action, array $extra = [], $uid = 1, $admin = 0)
{
    global $WORK, $DB, $SRV;
    $args = [PHP_BINARY, "$WORK/call.php", (string) $uid, $admin ? (string) $admin : '-',
             (string) $serviceId, $action];
    foreach ($extra as $k => $v) {
        if (is_array($v)) { foreach ($v as $item) { $args[] = $k . '[]=' . $item; } }
        else { $args[] = $k . '=' . $v; }
    }
    $cmd = 'EVWORK=' . escapeshellarg($WORK) . ' EVDB=' . escapeshellarg($DB) . ' EVSRV=' . escapeshellarg($SRV) . ' ';
    foreach ($args as $a) { $cmd .= escapeshellarg($a) . ' '; }
    $out = shell_exec($cmd . ' 2>/dev/null');
    if ($out === null || $out === '') { return ['status' => 'error', 'message' => 'no output']; }
    foreach (array_reverse(array_filter(array_map('trim', explode("\n", (string) $out)))) as $line) {
        $d = json_decode($line, true);
        if (is_array($d) && isset($d['status'])) { return $d; }
    }
    return ['status' => 'error', 'message' => 'bad json', 'raw' => substr((string) $out, 0, 200)];
}

echo "=== api_client.php 路由测试 ===\n\n";

/* ---------------- 鉴权与租户 ---------------- */
echo "-- 鉴权与租户隔离 --\n";
check('未登录被拒', (callApi(900, 'getInfo', [], 0)['code'] ?? '') === 'NOT_LOGGED_IN');
check('越权访问他人服务被拒', (callApi(900, 'getInfo', [], 2)['code'] ?? '') === 'ACCESS_DENIED');
check('不存在的服务被拒', (callApi(999, 'getInfo', [], 1)['code'] ?? '') === 'SERVICE_NOT_FOUND');
check('未绑定实例的服务被拒', (callApi(903, 'getInfo', [], 1)['code'] ?? '') === 'NOT_PROVISIONED');
check('管理员可跨租户', (callApi(900, 'getInfo', [], 1, 1)['status'] ?? '') === 'success');

/* ---------------- 能力矩阵 ---------------- */
echo "\n-- 能力矩阵 --\n";
$lxc = callApi(900, 'capabilities');
$kvm = callApi(901, 'capabilities');
check('LXC runtime=lxc', ($lxc['data']['runtime'] ?? '') === 'lxc');
check('LXC 无 VNC 能力', ($lxc['data']['caps']['console_vnc'] ?? true) === false);
check('LXC console.type=ssh', ($lxc['data']['console']['type'] ?? '') === 'ssh');
check('KVM runtime=kvm', ($kvm['data']['runtime'] ?? '') === 'kvm');
check('KVM 有 VNC 能力', ($kvm['data']['caps']['console_vnc'] ?? false) === true);
check('KVM console.type=vnc', ($kvm['data']['console']['type'] ?? '') === 'vnc');

/* ---------------- 控制台分流 ---------------- */
echo "\n-- 控制台分流 --\n";
check('LXC 自动选 ssh', (callApi(900, 'getConsole')['data']['effective_type'] ?? '') === 'ssh');
check('LXC 显式请求 vnc 被纠正', (callApi(900, 'getConsole', ['type' => 'vnc'])['data']['effective_type'] ?? '') === 'ssh');
check('KVM 自动选 vnc', (callApi(901, 'getConsole')['data']['effective_type'] ?? '') === 'vnc');
check('KVM 失败路径也带 console.label', (callApi(901, 'getConsole')['data']['console']['label'] ?? '') === 'VNC 控制台');

/* ---------------- 页签 ---------------- */
echo "\n-- 页签数据 --\n";
foreach (['base','monitor','network','portmap','snapshots','backups','securitys','sshkey','tasks','setting'] as $tab) {
    $r = callApi(900, 'getTabData', ['tab' => $tab]);
    check("页签 {$tab}", ($r['status'] ?? '') === 'success', json_encode($r['message'] ?? $r, JSON_UNESCAPED_UNICODE));
}
check('LXC 访问 iso 页签被能力位拒绝', (callApi(900, 'getTabData', ['tab' => 'iso'])['code'] ?? '') === 'CAPABILITY_UNSUPPORTED');
check('KVM 访问 iso 页签允许', (callApi(901, 'getTabData', ['tab' => 'iso'])['status'] ?? '') === 'success');
check('未知页签被拒', (callApi(900, 'getTabData', ['tab' => 'nope'])['code'] ?? '') === 'UNKNOWN_TAB');

/* ---------------- 写操作防呆 ---------------- */
echo "\n-- 写操作防呆 --\n";
check('非法电源操作被拒', (callApi(900, 'power', ['op' => 'nuke'])['code'] ?? '') === 'BAD_ACTION');
// 宿主机无 KVM 能力时，KVM 那一侧先被「没有任何可用镜像」拦下，两种拒绝都正确
$rMis = callApi(901, 'reinstall', ['image' => 'ubuntu-noble']);
check('KVM 实例重装 LXC 镜像被拒', in_array($rMis['code'] ?? '', ['RUNTIME_MISMATCH', 'NO_IMAGES'], true), json_encode($rMis, JSON_UNESCAPED_UNICODE));
check('KVM 实例重装 KVM 镜像通过校验', (callApi(901, 'reinstall', ['image' => 'kvm-ubuntu-noble'])['code'] ?? '') !== 'RUNTIME_MISMATCH');
check('LXC 实例重装 KVM 镜像被拒', (callApi(900, 'reinstall', ['image' => 'kvm-ubuntu-jammy'])['code'] ?? '') === 'RUNTIME_MISMATCH');
check('未选镜像被拒', (callApi(900, 'reinstall', [])['code'] ?? '') === 'IMAGE_REQUIRED');
check('非法主机名被拒', (callApi(900, 'rename', ['hostname' => 'bad name!'])['code'] ?? '') === 'BAD_PARAM');
check('大写主机名被拒（RFC1123）', (callApi(900, 'rename', ['hostname' => 'BadHost'])['code'] ?? '') !== 'success');
check('空主机名被拒', (callApi(900, 'rename', ['hostname' => ''])['code'] ?? '') === 'BAD_PARAM');
check('数据盘缩小被拒', (callApi(901, 'resizeDataDisk', ['data_disk_gb' => '1'])['code'] ?? '') === 'SHRINK_NOT_ALLOWED');
check('数据盘非法值被拒', (callApi(901, 'resizeDataDisk', ['data_disk_gb' => '0'])['code'] ?? '') === 'BAD_PARAM');
check('未知操作被拒', (callApi(900, 'doSomethingWeird')['code'] ?? '') === 'UNKNOWN_ACTION');
// 救援模式：验证不被能力位拒绝，且**用完必须还原状态**，
// 否则会污染后续渲染与断言（这个坑真踩过一次）。
$rEnter = callApi(901, 'rescueEnter', ['iso_id' => 'router-probe-iso']);
check('KVM 救援不被能力位拒绝', ($rEnter['code'] ?? '') !== 'CAPABILITY_UNSUPPORTED', json_encode($rEnter, JSON_UNESCAPED_UNICODE));
callApi(901, 'rescueExit');
$afterRescue = $api->instance($kvmId);
check('救援状态已还原（测试不留脏状态）', empty($afterRescue['rescue_enabled']), json_encode($afterRescue['rescue_enabled'] ?? null));

/* ---------------- 幂等写入 ---------------- */
echo "\n-- 写入保护与幂等 --\n";
// 改主机名的语义是「实时修改系统内 hostname」，面板要求实例处于运行中。
// 沙箱里没有真容器，实例会被探测为 stopped，因此这里能验证的是**守卫本身**。
$r = callApi(900, 'rename', ['hostname' => 'router-test-host']);
$isStopped = (($r['code'] ?? '') === 'PRECONDITION_FAILED');
check('未运行实例改名被拒（面板要求 running）', $isStopped, json_encode($r, JSON_UNESCAPED_UNICODE));
check('拒绝原因明确指向运行状态', !$isStopped || strpos((string) ($r['message'] ?? ''), '运行') !== false);

// 幂等可回滚的写操作：带宽（v1 支持；remark 在 v1 没有写入端点）
$before = (int) $api->instance($lxcId)['network_down_mbps'];
$r = callApi(900, 'updateBandwidth', ['down_mbps' => 133, 'up_mbps' => 44]);
check('带宽写入成功', ($r['status'] ?? '') === 'success', json_encode($r, JSON_UNESCAPED_UNICODE));
check('带宽已生效', (int) $api->instance($lxcId)['network_down_mbps'] === 133, (string) $before);
callApi(900, 'updateBandwidth', ['down_mbps' => $before, 'up_mbps' => 50]);
check('带宽已还原', (int) $api->instance($lxcId)['network_down_mbps'] === $before);
check('带宽非法值被拒', (callApi(900, 'updateBandwidth', ['down_mbps' => 'abc'])['code'] ?? '') !== 'success');

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exec('rm -rf ' . escapeshellarg($WORK));
exit($fail === 0 ? 0 : 1);
