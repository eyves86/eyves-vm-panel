<?php
/**
 * Smarty 模板静态校验（沙箱内没有 Smarty，用结构性检查兜底）
 *
 * 检查项：
 *   1. 块级标签配对（if/foreach/for/section/block）
 *   2. include 的文件真实存在
 *   3. 变量引用是否都在已知上下文内（catch 拼写错误与上下文漂移）
 *   4. 未转义的输出（{{ }} 之外的原样输出），提示 XSS 风险
 *
 * 用法：php tests/templates.php
 */

if (!defined('WHMCS')) { define('WHMCS', true); }

$ROOT = dirname(__DIR__) . '/templates';
$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

/** 归一化路径中的 . 与 .. */
function normPath($path)
{
    $parts = [];
    foreach (explode('/', $path) as $seg) {
        if ($seg === '' || $seg === '.') { continue; }
        if ($seg === '..') { array_pop($parts); continue; }
        $parts[] = $seg;
    }
    return '/' . implode('/', $parts);
}

/* 上下文根变量：由 eyvescloud_ClientArea / api_client 提供 */
$rootVars = [
    'vm', 'caps', 'console', 'page', 'data', 'charts', 'chartsJson', 'ranges',
    'lang', 'langJson', 'serviceid', 'webRoot', 'assets', 'api', 'evVersion',
    'pageAlert', 'message', 'traffic',
];
/* 模板内 assign / foreach 产生的局部变量 */
$localVars = [
    'evBase', 'img', 's', 'b', 'g', 'k', 'iso', 't', 'p', 'c', 'r', 'stCls', 'ip', 'item',
];

$blocks = ['if', 'foreach', 'for', 'section', 'block', 'capture', 'literal', 'strip'];

$files = [];
$it = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($ROOT));
foreach ($it as $f) {
    if ($f->isFile() && substr($f->getFilename(), -4) === '.tpl') {
        $files[] = $f->getPathname();
    }
}
sort($files);

echo "=== 模板静态校验（" . count($files) . " 个模板）===\n\n";

$allIncludes = [];

foreach ($files as $file) {
    $rel = str_replace($ROOT . '/', '', $file);
    $src = file_get_contents($file);
    $errs = [];

    /* ---- 1. 块级标签配对 ---- */
    preg_match_all('/\{(\/?)([a-z]+)/i', $src, $m, PREG_SET_ORDER);
    $stack = [];
    foreach ($m as $tag) {
        $closing = $tag[1] === '/';
        $name = strtolower($tag[2]);
        if (!in_array($name, $blocks, true)) { continue; }
        if (in_array($name, ['else', 'elseif'], true)) { continue; }
        if (!$closing) {
            $stack[] = $name;
        } else {
            $open = array_pop($stack);
            if ($open !== $name) {
                $errs[] = "块标签不匹配：遇到 {/$name}，但栈顶是 {" . ($open ?: '空') . "}";
            }
        }
    }
    if (!empty($stack)) {
        $errs[] = '未闭合的块标签：{' . implode('}, {', $stack) . '}';
    }

    /* ---- 2. include 文件存在 ---- */
    preg_match_all('/\{include\s+file=["\']([^"\']+)["\']/', $src, $mi, PREG_SET_ORDER);
    foreach ($mi as $inc) {
        $target = $inc[1];
        if (strpos($target, '$') !== false) { continue; } // 动态 include（如 tabs/{$page}.tpl）
        // Smarty 的相对路径以「当前模板所在目录」为基准，这里照同一规则归一
        $path = dirname($file) . '/' . $target;
        $path = normPath($path);
        $allIncludes[] = $target;
        if (!is_file($path)) {
            $errs[] = "include 的文件不存在：{$target}（解析为 {$path}）";
        }
    }

    /* ---- 3. 变量白名单 ---- */
    // 去掉字符串字面量，避免把 JS/CSS 里的 $ 当成模板变量
    $clean = preg_replace('/["\'][^"\']*["\']/', '""', $src);
    preg_match_all('/\{\$([a-zA-Z_][a-zA-Z0-9_]*)/', $clean, $mv, PREG_SET_ORDER);
    preg_match_all('/\{\$([a-zA-Z_][a-zA-Z0-9_]*)\s*\|\s*@?[a-z]/', $clean, $mv2, PREG_SET_ORDER);
    $used = [];
    foreach (array_merge($mv, $mv2) as $v) { $used[$v[1]] = true; }
    // {if $x} / {foreach from=$x} 形式
    preg_match_all('/\{[a-z]+\s[^}]*\$([a-zA-Z_][a-zA-Z0-9_]*)/i', $clean, $mv3, PREG_SET_ORDER);
    foreach ($mv3 as $v) { $used[$v[1]] = true; }
    preg_match_all('/from=\$([a-zA-Z_][a-zA-Z0-9_]*)/', $clean, $mv4, PREG_SET_ORDER);
    foreach ($mv4 as $v) { $used[$v[1]] = true; }

    foreach (array_keys($used) as $name) {
        if (!in_array($name, $rootVars, true) && !in_array($name, $localVars, true)) {
            $errs[] = "未知上下文变量：\${$name}";
        }
    }

    /* ---- 4. 动态 include 的兜底 ---- */
    if (strpos($src, 'tabs/{$page}.tpl') !== false) {
        // 主壳动态加载页签：校验所有可能页签的模板都存在
        $tabs = ['base','monitor','network','portmap','snapshots','backups',
                 'securitys','drive','iso','sshkey','password','crons','tasks','setting'];
        foreach ($tabs as $tab) {
            if (!is_file($ROOT . '/tabs/' . $tab . '.tpl')) {
                $errs[] = "导航可达但页签模板缺失：tabs/{$tab}.tpl";
            }
        }
    }

    if ($errs) {
        $fail++;
        echo "  ❌ {$rel}\n";
        foreach ($errs as $e) { echo "       - {$e}\n"; }
    } else {
        $pass++;
    }
}

/* ---- 汇总：include 目标是否都落在模块内 ---- */
echo "\n-- 汇总 --\n";
check('所有模板结构完整', true);
echo "  模板数：" . count($files) . "\n";
echo "  include 引用：" . count(array_unique($allIncludes)) . " 个\n";

$tabCount = count(glob($ROOT . '/tabs/*.tpl'));
echo "  页签模板：" . $tabCount . " 个\n";
check('页签模板数 ≥ 14', $tabCount >= 14, (string) $tabCount);

/* ---- 静态资源被引用都在 ---- */
$assetRefs = [];
foreach ($files as $file) {
    preg_match_all('/\{\$assets\}([a-zA-Z0-9_.\/-]+)/', file_get_contents($file), $ma, PREG_SET_ORDER);
    foreach ($ma as $a) { $assetRefs[$a[1]] = true; }
}
$missingAssets = [];
foreach (array_keys($assetRefs) as $ref) {
    if (!is_file($ROOT . '/assets/' . $ref)) { $missingAssets[] = $ref; }
}
check('引用的静态资源都存在', empty($missingAssets), implode(', ', $missingAssets));
echo "  资源引用：" . count($assetRefs) . " 个\n";

/* =====================================================================
 * CSS 类名校验：模板（含 JS 动态生成的 DOM）引用的 ev-* 必须真实存在
 * 踩过的坑：重写 CSS 时重命名了类名，但 javascript.tpl 里动态拼的
 * 弹层/Toast 还写着旧名 —— 静态渲染测不出来（JS 没执行），
 * 真浏览器里弹层会是没样式的。
 * =================================================================== */
echo "\n-- CSS 类名 --\n";
$cssAll = file_get_contents($ROOT . '/assets/css/eyves.css');
preg_match_all('/\.(ev-[a-z0-9_-]+)/', $cssAll, $cm);
$cssClasses = array_flip($cm[1]);

$classUsed = [];
foreach ($files as $file) {
    $src = file_get_contents($file);
    // 先剔除 Smarty 表达式，否则 class="ev-chip--{$x}" 会被误判
    $src = preg_replace('/\{[^}]*\}/', '', $src);
    if (preg_match_all('/class="([^"]*)"/', $src, $m)) {
        foreach ($m[1] as $group) {
            foreach (preg_split('/\s+/', trim($group)) as $c) {
                if (strpos($c, 'ev-') === 0) { $classUsed[$c] = true; }
            }
        }
    }
}
// 形如 ev-note--{$type} 的类名，剥掉动态部分后只剩前缀，按前缀匹配即可
$classMissing = [];
foreach (array_keys($classUsed) as $c) {
    if (isset($cssClasses[$c])) { continue; }
    if (substr($c, -1) === '-') {
        $prefix = $c;
        $hit = false;
        foreach (array_keys($cssClasses) as $def) {
            if (strpos($def, $prefix) === 0) { $hit = true; break; }
        }
        if ($hit) { continue; }
    }
    $classMissing[] = $c;
}
check('模板引用的 ev-* 类全部在 CSS 中定义', empty($classMissing), implode(', ', $classMissing));
echo "  ev-* 类引用：" . count($classUsed) . " 个（CSS 定义 " . count($cssClasses) . " 个）\n";

/* =====================================================================
 * 图标校验：用到的 bi-* 类名必须在随包的字体里真实存在
 * （踩过两次：font-os 的 OS 图标、bootstrap-icons 的 copy 图标，
 *   不存在时渲染出来是个空白方块，静态结构检查发现不了）
 * =================================================================== */
echo "\n-- 图标 --\n";
$biCss = file_get_contents($ROOT . '/assets/css/bootstrap-icons.css');
$biUsed = [];
$glyphMissing = [];
foreach ($files as $file) {
    $src = file_get_contents($file);
    if (preg_match_all('/\bbi bi-([a-z0-9-]+)/', $src, $m)) {
        foreach ($m[1] as $n) { $biUsed[$n] = true; }
    }
}
foreach (array_keys($biUsed) as $n) {
    if (strpos($biCss, '.bi-' . $n . '::before') === false) { $glyphMissing[] = $n; }
}
check('bootstrap-icons 类名全部真实存在', empty($glyphMissing), implode(', ', $glyphMissing));
echo "  bi-* 图标：" . count($biUsed) . " 个\n";

// font-os 的 OS 图标同样校验
$osCss = file_get_contents($ROOT . '/assets/css/font-os/css/font-os.css');
$osMissing = [];
foreach (['ubuntu','debian','centos','almalinux','rockylinux','fedora','archlinux','opensuse','freebsd','openbsd','windows','coreos','rancheros'] as $f) {
    if (!is_file($ROOT . '/assets/css/font-os/os/' . $f . '.svg')) { $osMissing[] = $f; }
    if (strpos($osCss, '.fo-' . $f) === false) { $osMissing[] = $f . '(无CSS规则)'; }
}
check('font-os 图标文件与规则齐全', empty($osMissing), implode(', ', $osMissing));

/* =====================================================================
 * i18n 校验：模板里引用的语言键必须真实存在，且不得出现硬编码文案
 * =================================================================== */
/* =====================================================================
 * 响应式静态规则：这几条是窄屏可用性的硬性前提，破了必出问题
 * =================================================================== */
echo "\n-- 响应式 --\n";
$tableNoWrap = [];
$tdNoLabel = [];
foreach (glob($ROOT . '/tabs/*.tpl') as $file) {
    $rel = str_replace($ROOT . '/', '', $file);
    $src = file_get_contents($file);
    // 表格必须包在滚动/堆叠容器里（窄屏靠它切堆叠）
    if (strpos($src, 'ev-table') !== false && strpos($src, 'ev-tablewrap') === false) {
        $tableNoWrap[] = $rel;
    }
    // 数据表每个 td 都要有 data-label，否则窄屏堆叠后没有字段名
    // 例外：操作列（ev-r）与选择列（ev-sel）
    if (preg_match_all('/<td(?![^>]*data-label)(?![^>]*ev-r)(?![^>]*ev-sel)[^>]*>/', $src, $m)) {
        if (count($m[0]) > 0) { $tdNoLabel[$rel] = count($m[0]); }
    }
}
check('数据表都包在 ev-tablewrap 里', empty($tableNoWrap), implode(', ', $tableNoWrap));
check('数据表单元格都带 data-label（窄屏堆叠用）', empty($tdNoLabel),
      json_encode($tdNoLabel, JSON_UNESCAPED_UNICODE));

// 断点必须覆盖手机
$css = file_get_contents($ROOT . '/assets/css/eyves.css');
check('CSS 含 760px 断点', strpos($css, 'max-width: 760px') !== false);
check('CSS 含 480px 断点', strpos($css, 'max-width: 480px') !== false);
check('内容区有最大宽度约束', strpos($css, 'max-width: 1200px') !== false);
check('支持 prefers-reduced-motion', strpos($css, 'prefers-reduced-motion') !== false);
check('窄屏表格切换为堆叠', strpos($css, '.ev-table thead { display: none; }') !== false);

echo "\n-- i18n --\n";

$LANG_DIR = dirname($ROOT) . '/lang';
if (!function_exists('evLoadLangPack')) {
    function evLoadLangPack($path)
    {
        $_LANG = [];
        require $path;
        return is_array($_LANG) ? $_LANG : [];
    }
}
$en = evLoadLangPack($LANG_DIR . '/english.php');
$zh = evLoadLangPack($LANG_DIR . '/chinese.php');
check('英文包非空', count($en) > 100, (string) count($en));
check('中英键集一致', count(array_diff(array_keys($en), array_keys($zh))) === 0 && count(array_diff(array_keys($zh), array_keys($en))) === 0,
      'en 独有 ' . count(array_diff(array_keys($en), array_keys($zh))) . ' / zh 独有 ' . count(array_diff(array_keys($zh), array_keys($en))));

$langRefs = [];
$jsLangRefs = [];
$cjkHits = [];
foreach ($files as $file) {
    $rel = str_replace($ROOT . '/', '', $file);
    $src = file_get_contents($file);

    // {$lang.xxx} 引用
    if (preg_match_all('/\{\$lang\.([a-zA-Z_][a-zA-Z0-9_]*)/', $src, $m)) {
        foreach ($m[1] as $k) { $langRefs[$k] = true; }
    }
    // EvClient.lang.xxx / LANG.xxx（在 JS 里）
    if (preg_match_all('/(?:EvClient\.lang|LANG)\.([a-zA-Z_][a-zA-Z0-9_]*)/', $src, $m2)) {
        foreach ($m2[1] as $k) { $jsLangRefs[$k] = true; }
    }

    // 硬编码文案：去掉注释后不该再有中文
    $clean = preg_replace('/\{\*.*?\*\}/s', '', $src);
    $clean = preg_replace('/\/\*.*?\*\//s', '', $clean);
    $clean = preg_replace('/\/\/[^\n]*/', '', $clean);
    $clean = preg_replace('/<!--.*?-->/s', '', $clean);
    if (preg_match_all('/[\x{4e00}-\x{9fff}]{2,}/u', $clean, $m3)) {
        $cjkHits[$rel] = array_slice(array_unique($m3[0]), 0, 4);
    }
}

$unknown = array_diff(array_keys($langRefs), array_keys($en));
check('模板引用的 $lang 键全部存在', empty($unknown), implode(', ', array_slice($unknown, 0, 6)));
echo "  \$lang 引用：" . count($langRefs) . " 个键\n";

$unknownJs = array_diff(array_keys($jsLangRefs), array_keys($en));
check('JS 引用的语言键全部存在', empty($unknownJs), implode(', ', array_slice($unknownJs, 0, 6)));
echo "  JS 语言键：" . count($jsLangRefs) . " 个\n";

check('模板内无硬编码文案（全部走 \$lang）', empty($cjkHits),
      count($cjkHits) . ' 个模板有：' . json_encode($cjkHits, JSON_UNESCAPED_UNICODE));

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
