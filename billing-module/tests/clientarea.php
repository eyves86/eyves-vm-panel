<?php
/**
 * 客户区上下文测试：验证 ClientArea 为每个页签提供的变量完整、
 * 能力位把关有效、语言切换正常、URL 拼接正确。
 * 用法：php tests/clientarea.php [host] [port]
 */
require_once __DIR__ . '/whmcs_stub.php';

use WHMCS\Module\Server\EyvesCloud\StubStore;
use WHMCS\Module\Server\EyvesCloud\EyvesLang;

$host = isset($argv[1]) ? $argv[1] : '127.0.0.1';
$port = isset($argv[2]) ? (int) $argv[2] : 8998;

$pass = 0; $fail = 0;
function check($n, $c, $e = '') {
    global $pass, $fail;
    if ($c) { $pass++; echo "  ✅ $n\n"; } else { $fail++; echo "  ❌ $n" . ($e ? "  ($e)" : '') . "\n"; }
}

StubStore::reset();
StubStore::$tables['tblservers'][] = [
    'id' => 7, 'type' => 'eyvescloud', 'hostname' => $host, 'ipaddress' => $host,
    'port' => $port, 'secure' => 0, 'username' => 'admin',
    'password' => encrypt('Adapter#2026'), 'accesshash' => '{}',
];
StubStore::$tables['tblproducts'][] = ['id' => 31, 'name' => 'LXC 型'];
StubStore::$tables['tblproducts'][] = ['id' => 32, 'name' => 'KVM 型'];
StubStore::$tables['tblcustomfields'][] = ['id' => 501, 'relid' => 31, 'fieldname' => 'hostid'];
StubStore::$tables['tblcustomfields'][] = ['id' => 502, 'relid' => 32, 'fieldname' => 'hostid'];
StubStore::$tables['tblhosting'][] = ['id' => 900, 'userid' => 1, 'packageid' => 31, 'server' => 7, 'domain' => 'x'];
StubStore::$tables['tblhosting'][] = ['id' => 901, 'userid' => 1, 'packageid' => 32, 'server' => 7, 'domain' => 'y'];

require_once __DIR__ . '/../eyvescloud.php';

echo "=== 客户区上下文测试 ===\n\n";

/* 绑定真实实例 */
$api = new \WHMCS\Module\Server\EyvesCloud\EyvesCloud(['serverid' => 7]);
$lxcId = $kvmId = '';
foreach ($api->instances() as $it) {
    if ($it['name'] === 'ct-lxc-01') { $lxcId = (string) $it['id']; }
    if ($it['name'] === 'ct-kvm-01') { $kvmId = (string) $it['id']; }
}
eyvescloud_set_hostid(900, $lxcId, 31);
eyvescloud_set_hostid(901, $kvmId, 32);

$paramsLxc = ['serviceid' => 900, 'pid' => 31, 'serverid' => 7];
$paramsKvm = ['serviceid' => 901, 'pid' => 32, 'serverid' => 7];

/* ---------- 语言 ---------- */
echo "-- 语言包 --\n";
$_SESSION = ['Language' => 'english'];
EyvesLang::load();
check('英文包加载', EyvesLang::current() === 'english');
check('英文词条可用', EyvesLang::get('overview') === 'Overview');
$_SESSION = ['Language' => 'chinese'];
EyvesLang::load();
check('中文包加载', EyvesLang::current() === 'chinese');
check('中文词条覆盖英文', EyvesLang::get('overview') === '概览');
check('中文词条数完整', is_array(eyvescloud_lang_array()) && count(eyvescloud_lang_array()) >= 150, (string) count(eyvescloud_lang_array()));
$_SESSION = ['Language' => 'klingon'];
EyvesLang::load();
check('未知语言回落英文', EyvesLang::current() === 'english');
$_SESSION = ['Language' => 'zh-cn'];
EyvesLang::load();
check('语言别名 zh-cn → chinese', EyvesLang::current() === 'chinese');
check('get 缺键回落键名', EyvesLang::get('__no_such_key__') === '__no_such_key__');

/* ---------- URL 拼接 ---------- */
echo "\n-- URL 拼接 --\n";
check('assets URL 指向模块目录', strpos(eyvescloud_assets_url(), '/modules/servers/eyvescloud/templates/assets/') !== false, eyvescloud_assets_url());
check('api URL 指向 api_client.php', strpos(eyvescloud_api_url(), '/modules/servers/eyvescloud/api_client.php') !== false, eyvescloud_api_url());

/* ---------- OS 图标族识别 ---------- */
echo "\n-- OS 图标族 --\n";
check('ubuntu 识别', eyvescloud_os_family('ubuntu-noble') === 'ubuntu');
check('debian 识别', eyvescloud_os_family('debian-bookworm') === 'debian');
check('windows 识别', eyvescloud_os_family('windows-2022') === 'windows');
check('未知镜像返回空', eyvescloud_os_family('my-custom-image') === '');
check('空值不崩', eyvescloud_os_family('') === '');

/* ---------- ClientArea 上下文 ---------- */
echo "\n-- ClientArea 上下文 --\n";
$required = ['vm', 'caps', 'console', 'page', 'data', 'charts', 'chartsJson', 'ranges',
             'lang', 'langJson', 'serviceid', 'webRoot', 'assets', 'api', 'evVersion'];

$allPages = ['base', 'monitor', 'network', 'portmap', 'snapshots', 'backups',
             'securitys', 'drive', 'sshkey', 'password', 'crons', 'tasks', 'setting', 'iso'];

$ctxLxc = null;
foreach ($allPages as $p) {
    $_REQUEST['page'] = $p;
    $ctx = eyvescloud_ClientArea($paramsLxc);
    if ($p === 'base') { $ctxLxc = $ctx; }
    $vars = isset($ctx['vars']) ? $ctx['vars'] : [];
    $missing = array_diff($required, array_keys($vars));
    check("页签 {$p} 上下文完整", empty($missing), implode(',', $missing));
}

/* ---------- 能力位把关（服务端） ---------- */
echo "\n-- 能力位把关 --\n";
$_REQUEST['page'] = 'iso';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('LXC 手动访问 iso 页签被拒绝（回落 base）', ($ctx['vars']['page'] ?? '') === 'base', $ctx['vars']['page'] ?? '');

$_REQUEST['page'] = 'iso';
$ctx = eyvescloud_ClientArea($paramsKvm);
check('KVM 可正常访问 iso 页签', ($ctx['vars']['page'] ?? '') === 'iso', $ctx['vars']['page'] ?? '');

$_REQUEST['page'] = 'portmap';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('端口映射两种运行时都可用', ($ctx['vars']['page'] ?? '') === 'portmap');

$_REQUEST['page'] = '../../etc/passwd';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('非法 page 参数被清理', ($ctx['vars']['page'] ?? '') === 'base', $ctx['vars']['page'] ?? '');

/* ---------- 页签数据 ---------- */
echo "\n-- 页签数据 --\n";
$_REQUEST['page'] = 'setting';
$ctx = eyvescloud_ClientArea($paramsKvm);
$imgsK = $ctx['vars']['data']['images'] ?? [];
// 沙箱宿主机没有 KVM 能力，v1 会返回 0 个 KVM 镜像（正确行为）；
// 有则必须全是 kvm，无则不能混入 lxc。
$allKvm = true;
foreach ($imgsK as $i) { if (($i['runtime'] ?? '') !== 'kvm') { $allKvm = false; } }
check('KVM 设置页镜像不混入其它运行时', $allKvm && is_array($imgsK), count($imgsK) . ' 项');

$_REQUEST['page'] = 'setting';
$ctx = eyvescloud_ClientArea($paramsLxc);
$imgsL = $ctx['vars']['data']['images'] ?? [];
check('LXC 设置页带镜像清单', count($imgsL) > 0, count($imgsL) . ' 项');
$allLxc = true;
foreach ($imgsL as $i) { if (($i['runtime'] ?? '') !== 'lxc') { $allLxc = false; } }
check('LXC 设置页镜像全部为 lxc', $allLxc && count($imgsL) > 0, count($imgsL) . ' 项');

$_REQUEST['page'] = 'monitor';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('监控页带 ranges(4 档)', count($ctx['vars']['ranges'] ?? []) === 4);
check('监控页带 chartsJson（合法 JSON）', is_array(json_decode($ctx['vars']['chartsJson'], true)));
check('监控页 hasData 字段存在', array_key_exists('hasData', $ctx['vars']['data'] ?? []));

$_REQUEST['page'] = 'tasks';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('任务页带 tasks 数组', is_array($ctx['vars']['data']['tasks'] ?? null));

$_REQUEST['page'] = 'crons';
$ctx = eyvescloud_ClientArea($paramsLxc);
check('定时页带回退结构', array_key_exists('schedule', $ctx['vars']['data'] ?? []));

/* ---------- 派生字段 ---------- */
echo "\n-- 派生字段 --\n";
$_REQUEST['page'] = 'base';
$ctxK = eyvescloud_ClientArea($paramsKvm);
$vmK = $ctxK['vars']['vm'];
check('KVM status_label 已本地化', ($vmK['status_label'] ?? '') !== '', $vmK['status_label'] ?? '');
check('KVM os_family 已推导', ($vmK['os_family'] ?? '') === 'ubuntu', $vmK['os_family'] ?? '');
check('KVM region_code 字段存在', array_key_exists('region_code', $vmK));
check('模板文件为 clientarea', ($ctxK['templatefile'] ?? '') === 'templates/clientarea');

/* ---------- 状态标签本地化（曾经拼出不存在的键，显示原始英文串） ---------- */
echo "\n-- 状态标签本地化 --\n";
$statusExpect = [
    'running'  => ['chinese' => '运行中', 'english' => 'Running'],
    'stopped'  => ['chinese' => '已停止', 'english' => 'Stopped'],
    'creating' => ['chinese' => '创建中', 'english' => 'Creating'],
    'error'    => ['chinese' => '异常',   'english' => 'Failed'],
];
// 用真实的实例数据跑一遍：直接断言语言包里有对应键，且不等于原始英文状态串
foreach (['chinese', 'english'] as $lg) {
    $_SESSION['Language'] = $lg;
    EyvesLang::load();
    $L = eyvescloud_lang_array();
    $keyMap = ['running' => 'on', 'stopped' => 'off', 'creating' => 'creating', 'error' => 'status_error'];
    foreach ($keyMap as $st => $k) {
        $ok = isset($L[$k]) && $L[$k] !== '' && $L[$k] !== $st;
        check("{$lg}: 状态 {$st} → 词条 {$k} 存在且非原始串", $ok, $L[$k] ?? '(缺)');
        if (isset($statusExpect[$st][$lg])) {
            check("{$lg}: {$st} 文案为「{$statusExpect[$st][$lg]}」", ($L[$k] ?? '') === $statusExpect[$st][$lg], $L[$k] ?? '');
        }
    }
}
// 端到端：ClientArea 产出的 status_label 必须是本地化文案而非原始状态串
$_SESSION['Language'] = 'chinese';
$_REQUEST['page'] = 'base';
$ctxChk = eyvescloud_ClientArea($paramsLxc);
$lbl = $ctxChk['vars']['vm']['status_label'] ?? '';
$raw = $ctxChk['vars']['vm']['status'] ?? '';
check('ClientArea 的 status_label 已本地化（不等于原始状态串）', $lbl !== '' && $lbl !== $raw, "label={$lbl} raw={$raw}");

/* ---------- 未开通服务 ---------- */
echo "\n-- 边界 --\n";
StubStore::$tables['tblhosting'][] = ['id' => 950, 'userid' => 1, 'packageid' => 31, 'server' => 7, 'domain' => 'z'];
$ctx = eyvescloud_ClientArea(['serviceid' => 950, 'pid' => 31, 'serverid' => 7]);
check('未开通服务走 error 模板', ($ctx['templatefile'] ?? '') === 'templates/error');
check('error 模板也带 lang', isset($ctx['vars']['lang']));
check('error 模板也带 assets', isset($ctx['vars']['assets']));

echo "\n=== 结果: $pass 通过, $fail 失败 ===\n";
exit($fail === 0 ? 0 : 1);
