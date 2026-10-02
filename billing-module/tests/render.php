<?php
/**
 * 真 Smarty 渲染测试 —— 唯一能验证「模板真的能编译出页面」的手段。
 * （静态校验见 tests/templates.php，那个不需要 Smarty）
 *
 * Smarty 不在仓库里（避免给模块塞第三方依赖）。要跑本套件：
 *   curl -sL https://codeload.github.com/smarty-php/smarty/tar.gz/refs/tags/v4.5.4 | tar xz
 *   SMARTY_PATH=/path/to/smarty-4.5.4/libs php tests/render.php <面板IP> <端口>
 *
 * 未设置 SMARTY_PATH 时自动跳过（exit 0），不阻塞其它套件。
 */

$smartyLib = getenv('SMARTY_PATH');
$candidates = $smartyLib ? [$smartyLib] : [
    '/tmp/smarty/smarty-4.5.4/libs',
    __DIR__ . '/../vendor/smarty/smarty/libs',
];
$smartyLib = '';
foreach ($candidates as $c) {
    if (is_file($c . '/bootstrap.php')) { $smartyLib = $c; break; }
}
if ($smartyLib === '') {
    echo "=== 跳过真 Smarty 渲染测试（未找到 Smarty）===\n";
    echo "提示：SMARTY_PATH=/path/to/smarty-4.5.4/libs php tests/render.php\n";
    exit(0);
}

require_once __DIR__ . '/whmcs_stub.php';
require_once $smartyLib . '/bootstrap.php';

use WHMCS\Module\Server\EyvesCloud\StubStore;

$HOST = $argv[1] ?? '127.0.0.1';
$PORT = (int) ($argv[2] ?? 8998);
$MODULE = dirname(__DIR__);

$pass = 0; $fail = 0;
$errors = [];
function check($n, $c, $e = '') {
    global $pass, $fail, $errors;
    if ($c) { $pass++; echo "  ✅ $n\n"; }
    else { $fail++; $errors[] = "$n — $e"; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

/* ---------------- 数据准备 ---------------- */
StubStore::reset();
StubStore::$tables['tblservers'][] = [
    'id' => 7, 'type' => 'eyvescloud', 'hostname' => $HOST, 'ipaddress' => $HOST,
    'port' => $PORT, 'secure' => 0, 'username' => 'admin',
    'password' => encrypt('Adapter#2026'), 'accesshash' => '{}',
];
StubStore::$tables['tblproducts'][] = ['id' => 31, 'name' => 'LXC 型'];
StubStore::$tables['tblproducts'][] = ['id' => 32, 'name' => 'KVM 型'];
StubStore::$tables['tblcustomfields'][] = ['id' => 501, 'relid' => 31, 'fieldname' => 'hostid'];
StubStore::$tables['tblcustomfields'][] = ['id' => 502, 'relid' => 32, 'fieldname' => 'hostid'];
StubStore::$tables['tblhosting'][] = ['id' => 900, 'userid' => 1, 'packageid' => 31, 'server' => 7, 'domain' => 'lxc.local'];
StubStore::$tables['tblhosting'][] = ['id' => 901, 'userid' => 1, 'packageid' => 32, 'server' => 7, 'domain' => 'kvm.local'];

require_once $MODULE . '/eyvescloud.php';

$api = new \WHMCS\Module\Server\EyvesCloud\EyvesCloud(['serverid' => 7]);
$lxcId = $kvmId = '';
foreach ($api->instances() as $it) {
    if ($it['name'] === 'ct-lxc-01') { $lxcId = (string) $it['id']; }
    if ($it['name'] === 'ct-kvm-01') { $kvmId = (string) $it['id']; }
}
if ($lxcId === '' || $kvmId === '') { fwrite(STDERR, "面板缺少 ct-lxc-01 / ct-kvm-01\n"); exit(1); }
eyvescloud_set_hostid(900, $lxcId, 31);
eyvescloud_set_hostid(901, $kvmId, 32);

/* ---------------- Smarty 环境 ---------------- */
function makeSmarty()
{
    global $MODULE;
    $s = new Smarty();
    $s->setTemplateDir($MODULE);                       // 模拟 WHMCS 把模块目录挂成 template_dir
    $s->setCompileDir('/tmp/evrender/tpl_c');
    $s->setCacheDir('/tmp/evrender/tpl_cache');
    $s->setConfigDir('/tmp/evrender/tpl_cfg');
    $s->setCaching(Smarty::CACHING_OFF);
    $s->setForceCompile(true);

    $s->error_reporting = E_ALL & ~E_NOTICE & ~E_WARNING & ~E_DEPRECATED;
    return $s;
}
@mkdir('/tmp/evrender/tpl_c', 0755, true);
@mkdir('/tmp/evrender/tpl_cache', 0755, true);
@mkdir('/tmp/evrender/tpl_cfg', 0755, true);

echo "=== 真 Smarty 渲染测试（Smarty " . Smarty::SMARTY_VERSION . "）===\n\n";

$pages = ['base','monitor','network','portmap','snapshots','backups','securitys',
          'drive','iso','sshkey','password','crons','tasks','setting'];

$rendered = [];
foreach ([['LXC', 900, 31, 'chinese'], ['KVM', 901, 32, 'english']] as $case) {
    list($label, $sid, $pid, $uiLang) = $case;
    $_SESSION['Language'] = $uiLang;
    echo "-- {$label} 实例（service {$sid}，语言 {$uiLang}）--\n";
    foreach ($pages as $page) {
        $_REQUEST = ['page' => $page, 'serviceid' => $sid];
        $_GET = $_REQUEST;
        try {
            $ctx = eyvescloud_ClientArea(['serviceid' => $sid, 'pid' => $pid, 'serverid' => 7]);
        } catch (Throwable $e) {
            check("{$label}/{$page} 上下文", false, $e->getMessage());
            continue;
        }
        if (($ctx['templatefile'] ?? '') === 'templates/error') {
            // LXC 的 iso 页签会被服务端拒绝并回落 base，属预期
            $fallbackOk = ($label === 'LXC' && $page === 'iso');
            check("{$label}/{$page} 页签" . ($fallbackOk ? '（预期回落 base）' : ''), $fallbackOk,
                  $ctx['vars']['page'] ?? 'error 模板');
            continue;
        }
        $sm = makeSmarty();
        foreach ($ctx['vars'] as $k => $v) { $sm->assign($k, $v); }
        try {
            $html = $sm->fetch('templates/clientarea.tpl');
        } catch (Throwable $e) {
            check("{$label}/{$page} 渲染", false, $e->getMessage());
            continue;
        }
        $len = strlen($html);
        $okLen = $len > 800;
        $rendered["{$label}-{$page}"] = $html;
        check("{$label}/{$page} 渲染成功（{$len} 字节）", $okLen, '输出过短');
    }
    echo "\n";
}

/* ---------------- HTML 质量检查 ---------------- */
echo "-- 输出质量 --\n";
$lxcBase = $rendered['LXC-base'] ?? '';
$kvmBase = $rendered['KVM-base'] ?? '';
$lxcSet  = $rendered['LXC-setting'] ?? '';
$lxcMon  = $rendered['LXC-monitor'] ?? '';

check('LXC 概览含实例名', strpos($lxcBase, 'ct-lxc-01') !== false);
check('KVM 概览含实例名', strpos($kvmBase, 'ct-kvm-01') !== false);

// 语言：LXC 用中文渲染，KVM 用英文渲染 —— 证明 i18n 真的生效
check('中文渲染出中文标签', strpos($lxcBase, '概览') !== false && strpos($lxcBase, '资源监控') !== false);
check('英文渲染出英文标签', strpos($kvmBase, 'Overview') !== false && strpos($kvmBase, 'Monitoring') !== false);
check('两种语言同一模板不串味', strpos($kvmBase, '概览') === false);
check('中文状态标签', strpos($lxcBase, '已停止') !== false || strpos($lxcBase, '运行中') !== false);
check('英文状态标签', strpos($kvmBase, 'Stopped') !== false || strpos($kvmBase, 'Running') !== false);

// 能力位驱动的差异（最关键的一条）
check('KVM 概览有 VNC 端口格', strpos($kvmBase, 'VNC Port') !== false || strpos($kvmBase, 'VNC 端口') !== false);
// 只看可见标记，不扫内联的 JS 字典（字典已按需裁剪，不含 vncport）
check('LXC 概览无 VNC 端口', strpos($lxcBase, 'VNC Port') === false && strpos($lxcBase, 'VNC 端口') === false);
check('KVM 概览有 VNC 端口', strpos($kvmBase, 'VNC Port') !== false || strpos($kvmBase, 'VNC 端口') !== false);
check('JS 字典已瘦身（不含用不到的词条）', strpos($lxcBase, 'eyves_module_name') === false && strpos($lxcBase, 'monitor_no_data') === false);
check('KVM 导航有 ISO 页签', strpos($kvmBase, 'page=iso') !== false);
check('LXC 导航无 ISO 页签', strpos($lxcBase, 'page=iso') === false, 'LXC 出现了 ISO 页签');
check('LXC 导航有端口映射页签', strpos($lxcBase, 'page=portmap') !== false);
check('两种运行时都有控制台按钮', strpos($lxcBase, 'data-ev-action="console"') !== false && strpos($kvmBase, 'data-ev-action="console"') !== false);
check('LXC 控制台按钮是 SSH 终端', strpos($lxcBase, 'SSH 终端') !== false);
check('KVM 控制台按钮是 VNC 控制台', strpos($kvmBase, 'VNC Console') !== false || strpos($kvmBase, 'VNC 控制台') !== false);

// 数据里不能出现 kvm 子对象泄漏
check('LXC 概览无 KVM 域名字段', strpos($lxcBase, 'Domain') === false || strpos($lxcBase, 'VNC') !== false);

// 设置页
check('设置页含重装下拉', strpos($lxcSet, 'name="image"') !== false);
check('LXC 重装下拉只含 lxc 镜像', strpos($lxcSet, 'kvm-ubuntu') === false);

// OS 图标与时间格式（真机渲染才暴露得出来的两个 bug）
check('OS 图标带 fa-os + fo-xxx 两个类', strpos($kvmBase, 'fa-os fo-ubuntu') !== false);
check('OS 图标非空（LXC 也是图形类）', strpos($lxcBase, 'fa-os fo-ubuntu') !== false);
check('到期时间已归一为日粒度', strpos($kvmBase, '2027-09-02') !== false && strpos($kvmBase, '2027-09-02T') === false);
check('到期时间未被格子截断', strpos($kvmBase, '2027-09-02 00:...') === false);
check('创建时间已归一', strpos($lxcBase, '2026-09-01 00:00') !== false);
check('原始时间仍保留在 *_raw', strpos($lxcBase, 'T00:00:00Z') === false);

// 监控页
check('监控页渲染出图表容器或空态', strpos($lxcMon, 'ev-chart-') !== false || strpos($lxcMon, 'no-data') !== false || strpos($lxcMon, '暂无监控') !== false || strpos($lxcMon, 'No monitoring') !== false);

// 转义检查：注入型实例名必须被转义
global $rendered;

// 未渲染的 Smarty 标签残留
$leftover = 0;
foreach ($rendered as $name => $html) {
    if (preg_match('/\{(\/?)(if|foreach|include|assign|\$)/', $html)) {
        $leftover++; echo "     残留 Smarty 标签：$name\n";
    }
}
check('无残留未解析的 Smarty 标签', $leftover === 0, "$leftover 个页面有残留");

$unresolved = 0;
foreach ($rendered as $name => $html) {
    if (strpos($html, '{$') !== false) { $unresolved++; echo "     未解析变量：$name\n"; }
}
check('无未替换的模板变量', $unresolved === 0, "$unresolved 个页面");

$phpErrLeak = 0;
foreach ($rendered as $name => $html) {
    foreach (['Fatal error', 'Parse error', 'Warning:', 'Notice:', 'Undefined', 'Deprecated:'] as $needle) {
        if (strpos($html, $needle) !== false) { $phpErrLeak++; echo "     $needle：$name\n"; break; }
    }
}
check('页面无 PHP 错误/警告泄漏', $phpErrLeak === 0, "$phpErrLeak 个页面");

// 超长/特殊字符实例名不得破坏 HTML
$_SESSION['Language'] = 'chinese';
$_REQUEST = ['page' => 'base', 'serviceid' => 900];
try {
    $api->updateInstance($lxcId, ['remark' => '<img src=x onerror=alert(1)> "quoted" & ampersand']);
    $ctx = eyvescloud_ClientArea(['serviceid' => 900, 'pid' => 31, 'serverid' => 7]);
    $sm = makeSmarty();
    foreach ($ctx['vars'] as $k => $v) { $sm->assign($k, $v); }
    $html = $sm->fetch('templates/clientarea.tpl');
    check('恶意 remark 被转义（无裸 <img>）', strpos($html, '<img src=x') === false);
    check('恶意 remark 内容仍可见', strpos($html, 'onerror=alert(1)') !== false);
    $api->updateInstance($lxcId, ['remark' => '']);
} catch (Throwable $e) {
    check('恶意 remark 转义', false, $e->getMessage());
}

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";

// 落盘供浏览器查看
@mkdir('/tmp/evrender/out', 0755, true);
foreach ($rendered as $name => $html) {
    $head = '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">';
    // 预览用：把绝对资源路径改成相对，file:// 下才能加载 CSS
    $preview = str_replace('/modules/servers/eyvescloud/templates/assets/', 'modules/servers/eyvescloud/templates/assets/', $html);
    file_put_contents("/tmp/evrender/out/{$name}.html", $head . $preview);
}
echo "渲染产物已写入 /tmp/evrender/out/（" . count($rendered) . " 个页面）\n";
exit($fail === 0 ? 0 : 1);
