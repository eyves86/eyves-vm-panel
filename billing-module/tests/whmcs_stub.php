<?php
/**
 * 极简 WHMCS 桩：让模块在无 WHMCS 环境下可跑生命周期测试
 * 只实现本模块用到的 API 子集。
 */

namespace {
    define('WHMCS', true);
    define('ROOTDIR', sys_get_temp_dir() . '/eyves-whmcs-stub');

    if (!function_exists('encrypt')) {
        function encrypt($v) { return 'ENC:' . base64_encode((string) $v); }
    }
    if (!function_exists('decrypt')) {
        function decrypt($v) {
            $v = (string) $v;
            if (strpos($v, 'ENC:') === 0) {
                return base64_decode(substr($v, 4));
            }
            return $v;
        }
    }
}

namespace WHMCS\Module\Server\EyvesCloud {
    /** 内存表存储 */
    class StubStore
    {
        public static $tables = [];
        public static function reset()
        {
            self::$tables = [
                'tblservers' => [],
                'tblhosting' => [],
                'tblcustomfields' => [],
                'tblcustomfieldsvalues' => [],
                'tblproducts' => [],
            ];
        }
    }

    class StubQuery
    {
        protected $table;
        protected $wheres = [];
        protected $order = null;
        protected $limit = null;

        public function __construct($table) { $this->table = $table; }

        public function where($col, $val) { $this->wheres[] = [$col, $val]; return $this; }
        public function orderBy($col, $dir = 'asc') { $this->order = [$col, $dir]; return $this; }
        public function limit($n) { $this->limit = $n; return $this; }

        protected function match($row)
        {
            foreach ($this->wheres as $w) {
                $col = $w[0];
                if (!isset($row[$col]) || (string) $row[$col] !== (string) $w[1]) {
                    return false;
                }
            }
            return true;
        }

        protected function rows()
        {
            $rows = isset(StubStore::$tables[$this->table]) ? StubStore::$tables[$this->table] : [];
            $out = [];
            foreach ($rows as $r) { if ($this->match($r)) { $out[] = $r; } }
            if ($this->order) {
                usort($out, function ($a, $b) {
                    $c = $a[$this->order[0]] <=> $b[$this->order[0]];
                    return strtolower($this->order[1]) === 'desc' ? -$c : $c;
                });
            }
            if ($this->limit) { $out = array_slice($out, 0, $this->limit); }
            return $out;
        }

        public function first()
        {
            $r = $this->rows();
            return empty($r) ? null : (object) $r[0];
        }
        public function get() { return array_map(fn($r) => (object) $r, $this->rows()); }
        public function count() { return count($this->rows()); }

        public function insert(array $data)
        {
            $data['id'] = $data['id'] ?? (count(StubStore::$tables[$this->table]) + 1);
            StubStore::$tables[$this->table][] = $data;
            return true;
        }

        public function update(array $data)
        {
            $n = 0;
            foreach (StubStore::$tables[$this->table] as $i => $r) {
                if ($this->match($r)) {
                    StubStore::$tables[$this->table][$i] = array_merge($r, $data);
                    $n++;
                }
            }
            return $n;
        }

        public function delete()
        {
            $out = [];
            foreach (StubStore::$tables[$this->table] as $r) {
                if (!$this->match($r)) { $out[] = $r; }
            }
            $kept = count($out);
            $removed = count(StubStore::$tables[$this->table]) - $kept;
            StubStore::$tables[$this->table] = $out;
            return $removed;
        }
    }

    class StubCapsule
    {
        public static function table($name) { return new StubQuery($name); }
    }
}

namespace WHMCS\Database {
    class Capsule extends \WHMCS\Module\Server\EyvesCloud\StubCapsule {}
}
