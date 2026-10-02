<?php
/**
 * EyvesCloud WHMCS 模块 —— 语言加载
 *
 * 机制与原魔方模块一致：先加载 english.php 作为基底，
 * 再用当前语言包覆盖。这样任何语言包缺键都不会让模板炸掉。
 */

namespace {
    if (!defined('WHMCS')) {
        die('This file cannot be accessed directly');
    }
}

namespace WHMCS\Module\Server\EyvesCloud {

    class EyvesLang
    {
        /** @var bool */
        protected static $loaded = false;

        /** @var string */
        protected static $current = 'english';

        /** 语言包目录 */
        public static function langDir()
        {
            return dirname(__DIR__) . '/lang/';
        }

        /**
         * 加载语言包。$lang 为空时按 WHMCS 当前语言自动选择。
         */
        public static function load($lang = null)
        {
            global $_LANG, $CONFIG;

            $dir = self::langDir();
            $available = [];
            foreach ((array) glob($dir . '*.php') as $file) {
                $available[] = strtolower(basename($file, '.php'));
            }

            if (empty($lang)) {
                if (isset($_SESSION['Language']) && $_SESSION['Language'] !== '') {
                    $lang = $_SESSION['Language'];
                } elseif (isset($_SESSION['adminlang']) && $_SESSION['adminlang'] !== '') {
                    $lang = $_SESSION['adminlang'];
                } elseif (isset($CONFIG['Language']) && $CONFIG['Language'] !== '') {
                    $lang = $CONFIG['Language'];
                }
            }
            $lang = strtolower(trim((string) $lang));

            // 常见别名归一（WHMCS 语言目录名与我们的文件名不一定一致）
            $alias = [
                'chinese'            => 'chinese',
                'zh'                 => 'chinese',
                'zh-cn'              => 'chinese',
                'zh-hans'            => 'chinese',
                'simplified-chinese' => 'chinese',
                'english'            => 'english',
                'en'                 => 'english',
                'en-us'              => 'english',
            ];
            if (isset($alias[$lang])) {
                $lang = $alias[$lang];
            }

            $_LANG = (isset($_LANG) && is_array($_LANG)) ? $_LANG : [];

            // 基底：英文。必须先加载，保证任何语言包缺键都有英文兜底。
            self::merge($_LANG, $dir . 'english.php');

            if ($lang !== '' && $lang !== 'english' && in_array($lang, $available, true)) {
                self::merge($_LANG, $dir . $lang . '.php');
                self::$current = $lang;
            } else {
                self::$current = 'english';
            }

            self::$loaded = true;
            return self::$current;
        }

        /**
         * 把语言包并入目标数组。
         * 语言包内部写的是 $_LANG；在方法作用域里它是局部变量，
         * require 之后直接读回即可（不要用 global，否则会污染调用方）。
         */
        protected static function merge(&$lang, $file)
        {
            if (!is_file($file)) {
                return;
            }
            $_LANG = [];
            require $file;
            if (is_array($_LANG) && !empty($_LANG)) {
                $lang = array_merge($lang, $_LANG);
            }
        }

        public static function current()
        {
            return self::$current;
        }

        public static function isLoaded()
        {
            return self::$loaded;
        }

        /** 可用语言列表（供语言切换器使用） */
        public static function available()
        {
            $out = [];
            foreach ((array) glob(self::langDir() . '*.php') as $file) {
                $code = strtolower(basename($file, '.php'));
                $out[$code] = $code;
            }
            return $out;
        }

        /** 取词条，缺键时回落到默认值或键名本身 */
        public static function get($key, $default = null)
        {
            global $_LANG;
            if (isset($_LANG[$key]) && $_LANG[$key] !== '') {
                return $_LANG[$key];
            }
            return $default !== null ? $default : $key;
        }
    }
}

namespace {
    if (!function_exists('EyvesCloud_loadLang')) {
        /** 兼容原魔方模块的调用风格 */
        function EyvesCloud_loadLang($lang = null)
        {
            return \WHMCS\Module\Server\EyvesCloud\EyvesLang::load($lang);
        }
    }
}
