#!/bin/bash
# 从 install.sh 抽出真实代码片段，拼出一个自检脚本（只读，不改 install.sh）。
set -eu
SRC="${1:-/tmp/install_lf.sh}"
OUT="${2:-/tmp/inst_test.sh}"

awk '/^persisted_env_value\(\) \{/{p=1} /^install_service\(\) \{/{p=0} p' "$SRC" > "$OUT"

cat >> "$OUT" <<'HEADER'
# ---- 上面是从 install.sh 抽取的真实函数 ----
HEADER

# 重新拼：先写骨架（stub + 常量），再追加两个真实片段，最后追加断言
{
  cat <<'SKEL'
#!/bin/sh
# 自检：install.sh 新增的 env 文件助手 + systemd 单元拼装（代码从 install.sh 抽取，只读）
FAILS=0
ok()   { echo "PASS $1"; }
bad()  { echo "FAIL $1"; FAILS=$((FAILS + 1)); }
log()  { :; }
warn() { echo "WARN $*" >&2; }
die()  { echo "DIE $*" >&2; exit 1; }
has_cmd() { command -v "$1" >/dev/null 2>&1; }
is_systemd() { return 0; }
is_openrc()  { return 1; }
systemd_existing_units() { :; }
systemctl() { :; }
EYVESCLOUD_NETWORK_ENV="/tmp/it/network.env"
EYVESCLOUD_STORE_ENV="/tmp/it/store.env"
SKEL

  echo "# ==== 抽取：env 文件助手 ===="
  awk '/^# persisted_env_value /{p=1} /^install_service\(\) \{/{p=0} p' "$SRC"

  echo "# ==== 抽取：install_systemd_service（改写输出路径，避免碰真 /etc/systemd）===="
  awk '/^install_systemd_service\(\) \{/{p=1} /^install_openrc_service\(\) \{/{p=0} p' "$SRC" \
    | sed 's#/etc/systemd/system/eyvescloud.service#/tmp/it/eyvescloud.service#g'

  echo "# ==== 抽取：install_service（改写 redis.env 路径，避免碰真 /etc/eyvescloud）===="
  awk '/^install_service\(\) \{/{p=1} /^harden_data_dir_perms\(\) \{/{p=0} p' "$SRC" \
    | sed 's#/etc/eyvescloud/redis.env#/tmp/it/redis.env#g'

  cat <<'ASRT'

# ---------------- 断言 ----------------
rm -rf /tmp/it
mkdir -p /tmp/it

# 1) 全新安装、什么都没传：不建文件
if write_optional_env_file "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS; then
    bad "空环境不应创建 store.env"
else
    [ -f "$EYVESCLOUD_STORE_ENV" ] && bad "空环境却建了 store.env" || ok "空环境不建 store.env"
fi

# 2) 传了值：建文件、0600、内容正确
EYVESCLOUD_PG_DSN="postgres://u:p@h:5432/db"
EYVESCLOUD_CELL_DSNS="cell-1=/tmp/it/a.db, cell-2=postgres://h/b"
if write_optional_env_file "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS; then
    ok "有值时创建 store.env"
else
    bad "有值却未创建 store.env"
fi
perm="$(stat -c '%a' "$EYVESCLOUD_STORE_ENV" 2>/dev/null)"
[ "$perm" = "600" ] && ok "store.env 权限 0600" || bad "store.env 权限应为 0600，实际 $perm"
[ "$(persisted_env_value "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN)" = "postgres://u:p@h:5432/db" ] \
    && ok "PG_DSN 回读一致" || bad "PG_DSN 回读不一致"
grep -q '^EYVESCLOUD_CELL_DSNS=cell-1=/tmp/it/a.db, cell-2=postgres://h/b$' "$EYVESCLOUD_STORE_ENV" \
    && ok "CELL_DSNS 原样落盘" || bad "CELL_DSNS 落盘内容不符"

# 3) 升级场景：变量全未设置 ⇒ 保留既有文件（不能静默丢配置）
unset EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS
before="$(cat "$EYVESCLOUD_STORE_ENV")"
if write_optional_env_file "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS; then
    [ "$before" = "$(cat "$EYVESCLOUD_STORE_ENV")" ] && ok "未设置变量时保留既有 store.env" || bad "既有 store.env 被改写"
else
    bad "未设置变量时不应报告无配置（既有的还在）"
fi

# 4) 显式设成空串 ⇒ 清除（删文件）
EYVESCLOUD_PG_DSN=""
EYVESCLOUD_CELL_DSNS=""
if write_optional_env_file "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS; then
    bad "显式置空应删除 store.env"
else
    [ -f "$EYVESCLOUD_STORE_ENV" ] && bad "显式置空后文件仍在" || ok "显式置空删除 store.env"
fi
unset EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS

# 5) 不可打印字符（换行）⇒ 跳过该项，不留注入面
EYVESCLOUD_PG_DSN="$(printf 'postgres://ok\nEnvironment=EVIL=1')"
if write_optional_env_file "$EYVESCLOUD_STORE_ENV" EYVESCLOUD_PG_DSN; then
    bad "含换行的值应被整体拒绝"
else
    ok "含换行的值被拒绝（未落盘）"
fi
unset EYVESCLOUD_PG_DSN

# 6) load_persisted_env：补未设置项、不覆盖已显式设置的项
mkdir -p /tmp/it
printf 'EYVESCLOUD_PG_DSN=postgres://persisted\nEYVESCLOUD_NODE_TOKEN_KEY=0000000000000000000000000000000000000000000000000000000000000000\n' > "$EYVESCLOUD_STORE_ENV"
EYVESCLOUD_PG_DSN="postgres://from-argv"
unset EYVESCLOUD_NODE_TOKEN_KEY
load_persisted_env
[ "$EYVESCLOUD_PG_DSN" = "postgres://from-argv" ] && ok "load_persisted_env 不覆盖显式值" || bad "显式值被持久化值覆盖"
[ "$EYVESCLOUD_NODE_TOKEN_KEY" = "0000000000000000000000000000000000000000000000000000000000000000" ] \
    && ok "load_persisted_env 补入未设置项" || bad "未设置项未被补入（实际 '$EYVESCLOUD_NODE_TOKEN_KEY'）"
unset EYVESCLOUD_PG_DSN EYVESCLOUD_NODE_TOKEN_KEY

# 7) report_cell_dsns：点名面板会忽略的项
out="$(report_cell_dsns 'cell-1=/a.db,bogus,=nodb,cell-4=' 2>&1)"
case "$out" in *bogus*) ok "report_cell_dsns 点名缺 '=' 项" ;; *) bad "未点名缺 '=' 项：$out" ;; esac
case "$out" in *=nodb*) ok "report_cell_dsns 点名空 id 项" ;; *) bad "未点名空 id 项：$out" ;; esac

# 8) systemd 单元：每个可选 Environment 各自独占一行（历史 bug：AUTO_UPDATE 会吃掉 network.env）
EYVESCLOUD_AUTO_UPDATE=120
EYVESCLOUD_GOMEMLIMIT=96GiB
EYVESCLOUD_GOMAXPROCS=32
install_systemd_service >/dev/null 2>&1
unit=/tmp/it/eyvescloud.service
if [ -f "$unit" ]; then
    ok "单元文件已生成"
    grep -q '^Environment=EYVESCLOUD_AUTO_UPDATE=120$' "$unit" && ok "AUTO_UPDATE 独占一行" || bad "AUTO_UPDATE 行异常"
    grep -q '^Environment=GOMEMLIMIT=96GiB$' "$unit" && ok "GOMEMLIMIT 独占一行" || bad "GOMEMLIMIT 行异常"
    grep -q '^Environment=GOMAXPROCS=32$' "$unit" && ok "GOMAXPROCS 独占一行" || bad "GOMAXPROCS 行异常"
    grep -q '^EnvironmentFile=-/tmp/it/network.env$' "$unit" && ok "network.env 仍在（未被吃掉）" || bad "network.env 丢了"
    grep -q '^EnvironmentFile=-/etc/eyvescloud/redis.env$' "$unit" && ok "redis.env 已挂载" || bad "redis.env 丢失"
    grep -q '^EnvironmentFile=-/tmp/it/store.env$' "$unit" && ok "store.env 已挂载" || bad "store.env 未挂载"
else
    bad "单元文件未生成"
fi
# 非法取值必须挡掉（unit 注入面）
unset EYVESCLOUD_GOMEMLIMIT
EYVESCLOUD_GOMEMLIMIT='96GiB
Environment=EVIL=1'
install_systemd_service >/dev/null 2>&1
if grep -q 'EVIL' "$unit"; then bad "非法 GOMEMLIMIT 注入了单元"; else ok "非法 GOMEMLIMIT 被拒（无注入）"; fi

# 9) install_service 的 Redis 分支：非法地址必须真的按单机处理（不得把它写回 redis.env）
rm -rf /tmp/it; mkdir -p /tmp/it
unset EYVESCLOUD_STORE_ENV EYVESCLOUD_PG_DSN EYVESCLOUD_CELL_DSNS
unset EYVESCLOUD_NODE_TOKEN_KEY EYVESCLOUD_AGENT_GATEWAY_ADDR
unset EYVESCLOUD_GOMEMLIMIT EYVESCLOUD_GOMAXPROCS EYVESCLOUD_AUTO_UPDATE
EYVESCLOUD_REDIS_ADDR="[::1]:6379"
EYVESCLOUD_REDIS_PASSWORD="secret"
install_service >/dev/null 2>&1
if grep -q '::1' /tmp/it/redis.env 2>/dev/null; then
    bad "非法 Redis 地址被写进了 redis.env"
else
    ok "非法 Redis 地址未落盘（按单机模式）"
fi

# 10) 合法地址 ⇒ 落盘 0600 且回读一致
: > /tmp/it/redis.env
chmod 644 /tmp/it/redis.env
EYVESCLOUD_REDIS_ADDR="127.0.0.1:6379"
EYVESCLOUD_REDIS_PASSWORD="p@ss"
install_service >/dev/null 2>&1
[ "$(persisted_env_value /tmp/it/redis.env EYVESCLOUD_REDIS_ADDR)" = "127.0.0.1:6379" ] \
    && ok "合法 Redis 地址落盘且回读一致" || bad "合法 Redis 地址落盘异常"
[ "$(stat -c '%a' /tmp/it/redis.env 2>/dev/null)" = "600" ] && ok "redis.env 权限 0600" || bad "redis.env 权限非 0600"
unset EYVESCLOUD_REDIS_ADDR EYVESCLOUD_REDIS_PASSWORD

echo "-----"
if [ "$FAILS" = 0 ]; then echo "ALL-PASS"; else echo "FAILURES=$FAILS"; fi
exit "$FAILS"
ASRT
} > "$OUT"
chmod +x "$OUT"
echo "generated $OUT"
