<?php
/**
 * 瞬时故障重试的回归测试。
 *
 * 起一个桩服务器：第 1 次请求返回 500，之后返回 200，并把请求计数写入文件。
 * 断言：
 *   - GET 命中 5xx 会重试一次并最终成功（且**只**重试一次，不无限循环）
 *   - POST 不会被重试（避免写操作被重复执行）
 *   - 连接错误同样会重试
 *
 * 用法：php tests/retry.php
 */
require_once __DIR__ . '/../lib/EyvesCloud.php';

use WHMCS\Module\Server\EyvesCloud\EyvesCloud;

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

$PORT = 18731;
$DIR  = sys_get_temp_dir() . '/ev-retry-' . getmypid();
@mkdir($DIR, 0755, true);
$COUNT = $DIR . '/count';
@unlink($COUNT);

echo "=== 瞬时故障重试测试 ===\n\n";

/* ---------------- 桩服务器 ---------------- */
file_put_contents($DIR . '/mock.py', <<<'PY'
import http.server, json, os, sys, threading

PORT = int(sys.argv[1]); COUNT = sys.argv[2]; MODE = sys.argv[3]

class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def _n(self):
        try:
            n = int(open(COUNT).read())
        except Exception:
            n = 0
        n += 1
        open(COUNT, 'w').write(str(n))
        return n
    def _handle(self):
        n = self._n()
        code = 200 if (MODE == 'fail_once' and n > 1) else (500 if MODE == 'fail_once' else 503)
        body = json.dumps({"success": code == 200, "code": "OK" if code == 200 else "UPSTREAM",
                           "data": {"n": n}}).encode()
        self.send_response(code)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def do_GET(self): self._handle()
    def do_POST(self): self._handle()

srv = http.server.HTTPServer(('127.0.0.1', PORT), H)
srv.serve_forever()
PY);

$mode = $argv[1] ?? 'fail_once';

/* ---------------- 用例 1：GET 首次 500，重试后成功 ---------------- */
$pids = [];
$spawn = function ($mode, $countFile) use ($PORT, $DIR, &$pids, &$kill0) {
    // 必须先杀掉上一个桩进程，否则同端口被旧进程占着，
    // 新起的桩根本收不到请求（测试会假通过/假失败）。
    shell_exec("pkill -f 'mock.py {$PORT}' 2>/dev/null");
    usleep(300000);
    @unlink($countFile);
    $cmd = 'python3 ' . escapeshellarg($DIR . '/mock.py') . ' ' . $PORT . ' '
         . escapeshellarg($countFile) . ' ' . escapeshellarg($mode) . ' > /dev/null 2>&1 &';
    shell_exec($cmd);
    usleep(700000);
};
$kill = function () use ($PORT) { shell_exec("pkill -f 'mock.py {$PORT}' 2>/dev/null"); };

$spawn('fail_once', $COUNT);
$api = new EyvesCloud([], [
    'hostname' => '127.0.0.1', 'ipaddress' => '127.0.0.1', 'port' => $PORT, 'secure' => 0,
    'username' => 'admin', 'password' => 'x', 'accesshash' => json_encode(['api_key' => 'k']),
]);

$data = null;
try {
    $data = $api->call('GET', '/api/v2/instances');
    check('GET 遇 5xx 重试后成功', is_array($data), json_encode($data));
} catch (Throwable $e) {
    check('GET 遇 5xx 重试后成功', false, $e->getMessage());
}
$n = (int) @file_get_contents($COUNT);
check('GET 恰好请求 2 次（只重试一次）', $n === 2, "实际 {$n} 次");

/* ---------------- 用例 2：GET 持续 5xx，重试后仍失败且不挂死 ---------------- */
$spawn('always_fail', $COUNT);
$t0 = microtime(true);
$env = $api->raw('GET', '/api/v2/instances');
$elapsed = microtime(true) - $t0;
$n = (int) @file_get_contents($COUNT);
check('持续 5xx 时返回失败信封而非抛异常', $env['ok'] === false);
check('持续 5xx 恰好请求 2 次（不无限重试）', $n === 2, "实际 {$n} 次");
check('持续 5xx 在合理时间内返回', $elapsed < 5, round($elapsed, 2) . 's');

/* ---------------- 用例 3：POST 不重试（写操作不能被重复执行） ---------------- */
$spawn('fail_once', $COUNT);
try {
    $api->call('POST', '/api/v2/instances', ['name' => 'x']);
    check('POST 遇 5xx 直接失败（不重试）', false, '未抛出异常');
} catch (Throwable $e) {
    check('POST 遇 5xx 直接失败（不重试）', true);
}
$n = (int) @file_get_contents($COUNT);
check('POST 只请求 1 次', $n === 1, "实际 {$n} 次");

$kill();

/* ---------------- 用例 4：连接错误也重试 ---------------- */
$dead = new EyvesCloud([], [
    'hostname' => '127.0.0.1', 'ipaddress' => '127.0.0.1', 'port' => 59997, 'secure' => 0,
    'username' => 'admin', 'password' => 'x', 'accesshash' => json_encode(['api_key' => 'k', 'timeout' => 3]),
]);
$t0 = microtime(true);
$env = $dead->raw('GET', '/api/v2/instances');
$elapsed = microtime(true) - $t0;
check('连接错误返回 CURL_ERROR 信封', $env['code'] === 'CURL_ERROR', $env['code']);
check('连接错误不挂死（重试后返回）', $elapsed < 10, round($elapsed, 2) . 's');

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
shell_exec('rm -rf ' . escapeshellarg($DIR));
exit($fail === 0 ? 0 : 1);
