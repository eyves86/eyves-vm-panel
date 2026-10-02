#!/bin/sh
# 往面板库里种一枚 LXC + 一枚 KVM 实例，用于验证 WHMCS 模块的实例级功能。
#
# 面板加载 config 时会把每列 Scan 成 Go 的具体类型（string/int），
# 任何 NULL 都会让整个配置加载失败 —— 典型报错：
#   Failed to initialize config: sql: Scan error on column index 9,
#   name "mac_address": converting NULL to string is unsupported
# 因此种入后必须把所有 NULL 列补成 '' 或 0（见文末 fill_nulls.py）。
set -e

DB=/tmp/eyves-adapter/home/.eyvescloud/config.db

# 面板退出时 WAL 可能未落盘，先 checkpoint 再动主库，避免读到旧状态。
if [ -f "$DB" ]; then
  sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null 2>&1 || true
  rm -f "$DB-wal" "$DB-shm"
fi

mkdir -p /var/lib/lxc/ct-lxc-01 /var/lib/libvirt/images
touch /var/lib/libvirt/images/ct-kvm-01.qcow2 2>/dev/null || true

sqlite3 "$DB" <<'SQL'
DELETE FROM containers WHERE name IN ('ct-lxc-01','ct-kvm-01');

-- 注意：mac_address 必须给真实值。留空会让面板的容器加载器 Scan 失败。
INSERT INTO containers
 (id,uuid,name,virtualization,lxc_name,kvm_name,disk_image,mac_address,template,vcpu,ram_mb,disk_gb,
  data_disk_gb,network_down_mbps,network_up_mbps,monthly_traffic_gb,traffic_mode,
  status,ip,ssh_port,vnc_port,port_mapping_limit,snapshot_limit,created_at,expires_at)
VALUES
 (9101,'uuid-lxc-9101','ct-lxc-01','lxc','ct-lxc-01','','','02:00:00:00:91:01','ubuntu-noble',1.0,512,10,
  0,100,50,1000,'total',
  'running','10.0.3.101',22101,0,10,3,'2026-09-01T00:00:00Z','2027-09-01T00:00:00Z');

INSERT INTO containers
 (id,uuid,name,virtualization,lxc_name,kvm_name,disk_image,mac_address,template,vcpu,ram_mb,disk_gb,
  data_disk_gb,network_down_mbps,network_up_mbps,monthly_traffic_gb,traffic_mode,
  status,ip,ssh_port,vnc_port,port_mapping_limit,snapshot_limit,created_at,expires_at)
VALUES
 (9102,'uuid-kvm-9102','ct-kvm-01','kvm','','ct-kvm-01','/var/lib/libvirt/images/ct-kvm-01.qcow2','02:00:00:00:91:02',
  'kvm-ubuntu-jammy',2.0,2048,40,
  100,200,100,2000,'in_out',
  'running','10.0.3.102',22102,59102,5,5,'2026-09-02T00:00:00Z','2027-09-02T00:00:00Z');
SQL

# 给 LXC 实例塞一个 XSS 载荷，供渲染层验证模板转义（v1 没有写 remark 的 API，只能从库里种）
sqlite3 "$DB" "UPDATE containers SET remark='<img src=x onerror=alert(1)> & \"quoted\"' WHERE id=9101;"

echo "seeded:"; sqlite3 "$DB" "select id,name,virtualization,status from containers;"

# 把所有 NULL 列补成安全默认值。
# 坑：不能在迭代 PRAGMA 的同一个 cursor 上执行 UPDATE —— 迭代会被提前打断，
# 结果只填了前几列，后面仍留 NULL。必须先把列清单物化，且用独立 cursor。
python3 - "$DB" <<'PY'
import sqlite3, sys

con = sqlite3.connect(sys.argv[1])
read = con.cursor()
write = con.cursor()

cols = list(read.execute("PRAGMA table_info(containers)"))   # 先物化，避免迭代被 UPDATE 打断
filled = []
for _, name, typ, *_ in cols:
    if name == 'id':
        continue
    t = (typ or '').upper()
    empty = "''" if ('TEXT' in t or 'CHAR' in t or 'CLOB' in t) else '0'
    n = write.execute("UPDATE containers SET %s=%s WHERE %s IS NULL" % (name, empty, name)).rowcount
    if n:
        filled.append(name)

con.commit()

remaining = [n for _, n, *_ in cols
             if read.execute("SELECT COUNT(*) FROM containers WHERE %s IS NULL" % n).fetchone()[0]]
con.close()

print("filled NULL columns:", ", ".join(filled) if filled else "(none)")
if remaining:
    print("!! 仍有 NULL 的列（面板会启动失败）:", ", ".join(remaining))
    sys.exit(1)
print("no NULL columns remain")
PY

# 落盘并清 WAL，保证面板下次启动读到的是主库状态。
sqlite3 "$DB" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null 2>&1 || true
rm -f "$DB-wal" "$DB-shm"
