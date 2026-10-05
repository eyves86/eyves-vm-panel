#!/usr/bin/env bash
#
# fresh-install-e2e.sh — EyvesCloud 全新系统「安装 → 自检 → 卸载」一键验收
#
# 在一台**干净**的 Linux VM 上以 root 运行，全自动完成：
#   1) 从发行版 Release 安装（走真实下载 + SHA-256 + 签名校验路径）
#   2) 服务 / 端口 / API 功能自检（登录、只读接口、一次写操作）
#   3) 卸载并核对系统无残留
# 全过程写入一份报告目录并打包，便于回传分析。
#
# 用法（在干净 VM 上）：
#   sudo bash fresh-install-e2e.sh
#
# 可选环境变量：
#   EYVESCLOUD_VERSION=latest|vX.Y.Z        安装版本（默认 latest）
#   EYVESCLOUD_INSTALL_SH=/path/install.sh  使用本地 install.sh（默认从仓库 main 拉取）
#   EYVESCLOUD_E2E_LOCAL_BIN=/path/eyvescloud  用本地二进制安装（跳过下载/校验），
#                                           用于验证未发版的修复
#   EYVESCLOUD_E2E_KEEP=1                   跳过卸载（保留安装现场）
#   EYVESCLOUD_E2E_FORCE=1                  检测到已有安装时仍继续
#   EYVESCLOUD_E2E_ADMIN_USER / _ADMIN_PASS 首启凭据缺失时的回退登录账号
#   EYVESCLOUD_E2E_MODES=1                  追加覆盖 controller-only / agent-only 安装模式
#                                           （在主流程卸载后、同一台干净机上依次执行）
#   EYVESCLOUD_E2E_SKIP_IDEMPOTENT=1        跳过同版本重复安装的幂等断言
#
set -u

REPO_RAW_INSTALL_URL="https://raw.githubusercontent.com/eyves86/eyves-vm-panel/main/install.sh"
PANEL_PORT="${EYVESCLOUD_E2E_PORT:-8999}"
BASE="http://127.0.0.1:${PANEL_PORT}"
DATA_DIR="${EYVESCLOUD_DATA_DIR:-/root/.eyvescloud}"
CREDS_FILE="$DATA_DIR/initial-admin-credentials.txt"

# ---------------------------------------------------------------------------
# 让安装脚本走「非交互」分支：install.sh 用「能否写 /dev/tty」判断是否可交互。
# 若当前存在控制终端，则用 setsid 重新拉起到一个没有控制终端的新会话。
# ---------------------------------------------------------------------------
if [ -z "${EYVESCLOUD_E2E_NO_TTY:-}" ]; then
    if { printf '' >/dev/tty; } 2>/dev/null; then
        if command -v setsid >/dev/null 2>&1; then
            export EYVESCLOUD_E2E_NO_TTY=1
            if setsid --wait true >/dev/null 2>&1; then
                exec setsid --wait "$0" "$@" </dev/null
            fi
            exec setsid "$0" "$@" </dev/null
        fi
    fi
fi

REPORT_DIR="${EYVESCLOUD_E2E_REPORT_DIR:-/root/eyvescloud-e2e-$(date +%Y%m%d-%H%M%S)}"
mkdir -p "$REPORT_DIR" 2>/dev/null || REPORT_DIR="/tmp/eyvescloud-e2e-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$REPORT_DIR"
MAIN_LOG="$REPORT_DIR/e2e.log"
RESULTS="$REPORT_DIR/results.tsv"
: > "$MAIN_LOG"
: > "$RESULTS"
FAILS=0
WARNS=0

log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" | tee -a "$MAIN_LOG"; }
hdr()  { printf '\n===== %s =====\n' "$*" | tee -a "$MAIN_LOG"; }
record() { # record <PASS|FAIL|WARN|SKIP> <name> [detail]
    _s="$1"; _n="$2"; _d="${3:-}"
    printf '%s\t%s\t%s\n' "$_s" "$_n" "$_d" >> "$RESULTS"
    case "$_s" in
        PASS) : ;;
        WARN) WARNS=$((WARNS + 1)) ;;
        SKIP) : ;;
        *)    FAILS=$((FAILS + 1)) ;;
    esac
    log "[$_s] $_n${_d:+ — $_d}"
}
have() { command -v "$1" >/dev/null 2>&1; }

http_code() { # http_code <url> [curl extra args...]
    _url="$1"; shift
    curl -sS -m 15 -o /dev/null -w '%{http_code}' "$@" "$_url" 2>/dev/null || echo "000"
}
http_body() { # http_body <url> [curl extra args...]
    _url="$1"; shift
    curl -sS -m 15 "$@" "$_url" 2>/dev/null || true
}

if [ "$(id -u)" -ne 0 ]; then
    echo "请以 root 运行：sudo bash $0" >&2
    exit 1
fi

hdr "EyvesCloud 全新系统一键验收"
log "报告目录：$REPORT_DIR"
log "安装版本：${EYVESCLOUD_VERSION:-latest}"
log "无控制终端：$( [ -z "${EYVESCLOUD_E2E_NO_TTY:-}" ] && echo 否 || echo 是 )"

# ---------------------------------------------------------------------------
# 步骤 0：环境信息 + 预检
# ---------------------------------------------------------------------------
hdr "步骤 0：环境信息"
{
    echo "## uname"; uname -a
    echo "## os-release"; cat /etc/os-release 2>/dev/null
    echo "## cpu"; nproc 2>/dev/null; grep -m1 -oE 'vmx|svm' /proc/cpuinfo 2>/dev/null || echo "no hw-virt flag"
    echo "## virt"; systemd-detect-virt 2>/dev/null || echo unknown
    echo "## /dev/kvm"; [ -e /dev/kvm ] && echo present || echo absent
    echo "## mem"; free -m 2>/dev/null || true
    echo "## disk"; df -h / 2>/dev/null || true
    echo "## init"; ps -p 1 -o comm= 2>/dev/null || true
} > "$REPORT_DIR/env.txt" 2>&1
log "已采集环境信息 → env.txt"
have systemctl && record PASS "systemd 可用" || record FAIL "systemd 可用" "未检测到 systemctl"
have curl  || have wget || record FAIL "存在 curl/wget" "下载发行版需要"

# 预检冲突：端口占用 / 已有安装
port_busy=0
if have ss && ss -ltn 2>/dev/null | grep -q ":${PANEL_PORT}[[:space:]]"; then port_busy=1; fi
if have netstat && netstat -ltn 2>/dev/null | grep -q ":${PANEL_PORT}[[:space:]]"; then port_busy=1; fi
if [ -x /usr/local/bin/eyvescloud ] || [ -d "$DATA_DIR" ]; then
    if [ "${EYVESCLOUD_E2E_FORCE:-0}" != "1" ]; then
        record FAIL "干净环境（无既有安装）" "检测到已有 EyvesCloud；如需强制继续请设 EYVESCLOUD_E2E_FORCE=1"
        log "已中止：请使用一台干净 VM，或先卸载后重试。"
        exit 2
    fi
    record WARN "干净环境（无既有安装）" "存在既有安装，已按 FORCE 继续"
else
    record PASS "干净环境（无既有安装）"
fi
[ "$port_busy" = "0" ] && record PASS "端口 ${PANEL_PORT} 空闲" || record FAIL "端口 ${PANEL_PORT} 空闲" "已被占用"

# 记录卸载后应对照的敏感路径当前状态（用于残留判定）
LEFTOVER_PATHS="/etc/systemd/system/eyvescloud.service
/etc/systemd/system/eyvescloud-agent.service
/etc/systemd/system/eyvescloud-agent.service.disabled
/etc/init.d/eyvescloud
/etc/init.d/eyvescloud-agent
/usr/local/bin/eyvescloud
/usr/local/bin/vm
/root/.eyvescloud
/var/lib/eyvescloud
/etc/eyvescloud
/etc/sysctl.d/99-eyvescloud.conf"

# ---------------------------------------------------------------------------
# 步骤 1：取得 install.sh
# ---------------------------------------------------------------------------
hdr "步骤 1：准备 install.sh"
INSTALL_SH="${EYVESCLOUD_INSTALL_SH:-$REPORT_DIR/install.sh}"
if [ -f "$INSTALL_SH" ] && [ "${EYVESCLOUD_INSTALL_SH:-}" != "" ]; then
    record PASS "使用本地 install.sh" "$INSTALL_SH"
elif have curl; then
    if curl -fsSL -m 60 "$REPO_RAW_INSTALL_URL" -o "$INSTALL_SH" && [ -s "$INSTALL_SH" ]; then
        record PASS "下载 install.sh" "$REPO_RAW_INSTALL_URL"
    else
        record FAIL "下载 install.sh" "$REPO_RAW_INSTALL_URL"
    fi
elif have wget; then
    if wget -q -T 60 -O "$INSTALL_SH" "$REPO_RAW_INSTALL_URL" && [ -s "$INSTALL_SH" ]; then
        record PASS "下载 install.sh" "$REPO_RAW_INSTALL_URL"
    else
        record FAIL "下载 install.sh" "$REPO_RAW_INSTALL_URL"
    fi
else
    record FAIL "下载 install.sh" "无 curl/wget"
fi
if [ -s "$INSTALL_SH" ]; then
    cp -f "$INSTALL_SH" "$REPORT_DIR/install.sh.used" 2>/dev/null || true
    bash -n "$INSTALL_SH" 2>/dev/null && record PASS "install.sh 语法自检" || record FAIL "install.sh 语法自检"
else
    log "无法取得 install.sh，终止。"
    exit 2
fi

# ---------------------------------------------------------------------------
# 步骤 2：安装
#   默认走「真实下载 + 校验」；设 EYVESCLOUD_E2E_LOCAL_BIN=<二进制路径> 时用本地
#   构建（install.sh 见到 ./eyvescloud 即跳过下载），用于验证未发版的修复。
# ---------------------------------------------------------------------------
hdr "步骤 2：安装"
VER="${EYVESCLOUD_VERSION:-latest}"
export EYVESCLOUD_LOG_FILE="$REPORT_DIR/install-script.log"

INSTALL_CWD="$REPORT_DIR"
INSTALL_SH_RUN="$INSTALL_SH"
LOCAL_BIN_VERSION=""
if [ -n "${EYVESCLOUD_E2E_LOCAL_BIN:-}" ]; then
    if [ -f "$EYVESCLOUD_E2E_LOCAL_BIN" ]; then
        stage="$REPORT_DIR/stage"; mkdir -p "$stage"
        cp -f "$INSTALL_SH" "$stage/install.sh"
        cp -f "$EYVESCLOUD_E2E_LOCAL_BIN" "$stage/eyvescloud"; chmod +x "$stage/eyvescloud"
        INSTALL_CWD="$stage"; INSTALL_SH_RUN="$stage/install.sh"
        record PASS "使用本地二进制（跳过下载/校验）" "$EYVESCLOUD_E2E_LOCAL_BIN"
        log "本地二进制版本：$( "$stage/eyvescloud" --version 2>/dev/null | head -1 )"
        # 本地模式若未显式指定版本，则取二进制自报版本作为目标版本。
        # 原因：默认 latest 会去 Release API 解析目标 tag（当前发布版较旧），
        # 已装版本高于它时会触发「拒绝回退安装」，幂等断言无法进行。
        LOCAL_BIN_VERSION="$( "$stage/eyvescloud" --version 2>/dev/null \
            | sed -n 's/^EyvesCloud[[:space:]]*v\?//p' | head -1 )"
        if [ "$VER" = "latest" ] && [ -n "$LOCAL_BIN_VERSION" ]; then
            VER="$LOCAL_BIN_VERSION"
            log "本地模式已固定目标版本：$VER"
        fi
    else
        record FAIL "本地二进制存在" "$EYVESCLOUD_E2E_LOCAL_BIN"
        exit 2
    fi
else
    log "走发行版下载 + SHA-256/签名校验路径"
fi

install_rc=0
if ( cd "$INSTALL_CWD" && EYVESCLOUD_VERSION="$VER" bash "$INSTALL_SH_RUN" install ) >"$REPORT_DIR/install.log" 2>&1 </dev/null; then
    record PASS "安装脚本退出码 0"
else
    install_rc=$?
    record FAIL "安装脚本退出码 0" "rc=$install_rc，见 install.log"
fi
tail -n 5 "$REPORT_DIR/install.log" 2>/dev/null | sed 's/^/    install> /' | tee -a "$MAIN_LOG" >/dev/null

# --- 2a) 真实下载路径：断言「确实执行了哈希与签名校验」，防止校验被静默跳过 ---
# 注意：install.sh 的 run_step 会把每个步骤的 stdout/stderr 重定向到它自己的
# 日志文件（EYVESCLOUD_LOG_FILE，即 install-script.log），因此「校验通过」这类
# 步骤内日志**不在** install.log（后者只有 run_step 的 开始/完成 横幅）。
if [ -z "${EYVESCLOUD_E2E_LOCAL_BIN:-}" ]; then
    vlog="$REPORT_DIR/install-script.log"
    [ -s "$vlog" ] || vlog="$REPORT_DIR/install.log"
    if grep -q '校验清单签名验证通过（ed25519）' "$vlog" 2>/dev/null; then
        record PASS "真实下载：ed25519 清单签名验证通过"
    else
        record FAIL "真实下载：ed25519 清单签名验证通过" "$vlog 未见签名验证通过日志"
    fi
    if grep -qE '校验通过：.*SHA-256' "$vlog" 2>/dev/null; then
        record PASS "真实下载：SHA-256 校验通过"
    else
        record FAIL "真实下载：SHA-256 校验通过" "$vlog 未见 SHA-256 校验日志"
    fi
else
    record SKIP "真实下载：签名/哈希校验" "本地二进制模式跳过下载与校验"
fi

# --- 2b) 幂等：同版本重复安装应直接跳过（rc=0 且不重装）---
if [ "${EYVESCLOUD_E2E_SKIP_IDEMPOTENT:-0}" = "1" ]; then
    record SKIP "幂等：同版本重复安装被跳过" "EYVESCLOUD_E2E_SKIP_IDEMPOTENT=1"
elif [ "$install_rc" != "0" ]; then
    record SKIP "幂等：同版本重复安装被跳过" "首次安装未成功，跳过"
else
    if ( cd "$INSTALL_CWD" && EYVESCLOUD_LOG_FILE="$REPORT_DIR/install-rerun-script.log" \
            EYVESCLOUD_VERSION="$VER" bash "$INSTALL_SH_RUN" install ) \
            >"$REPORT_DIR/install-rerun.log" 2>&1 </dev/null; then
        if grep -q '无需重复安装' "$REPORT_DIR/install-rerun.log" "$REPORT_DIR/install-rerun-script.log" 2>/dev/null; then
            record PASS "幂等：同版本重复安装被跳过"
        else
            record WARN "幂等：同版本重复安装被跳过" "rc=0 但未出现「无需重复安装」提示"
        fi
    else
        record FAIL "幂等：同版本重复安装退出码 0" "rc=$?，见 install-rerun.log"
    fi
fi

# ---------------------------------------------------------------------------
# 步骤 3：服务与端口就绪
# ---------------------------------------------------------------------------
hdr "步骤 3：服务与端口就绪"
ready=0
for _ in $(seq 1 60); do
    if have systemctl && systemctl is-active --quiet eyvescloud 2>/dev/null; then ready=1; break; fi
    if have rc-service && rc-service eyvescloud status >/dev/null 2>&1; then ready=1; break; fi
    sleep 2
done
[ "$ready" = "1" ] && record PASS "服务已运行" || record FAIL "服务已运行" "等待超时"

port_up=0
for _ in $(seq 1 30); do
    if have ss && ss -ltn 2>/dev/null | grep -q ":${PANEL_PORT}[[:space:]]"; then port_up=1; break; fi
    if [ "$(http_code "$BASE/")" != "000" ]; then port_up=1; break; fi
    sleep 2
done
[ "$port_up" = "1" ] && record PASS "端口 ${PANEL_PORT} 已监听" || record FAIL "端口 ${PANEL_PORT} 已监听"

if have systemctl; then
    systemctl status eyvescloud --no-pager > "$REPORT_DIR/service-status.txt" 2>&1 || true
    journalctl -u eyvescloud -n 120 --no-pager > "$REPORT_DIR/journal.txt" 2>&1 || true
fi

# 二进制版本（以 CLI 为准）
if [ -x /usr/local/bin/eyvescloud ]; then
    ver_out="$(/usr/local/bin/eyvescloud --version 2>/dev/null | head -1)"
    log "已安装版本：${ver_out:-未知}"
    record PASS "二进制已安装" "${ver_out:-unknown}"
else
    record FAIL "二进制已安装" "/usr/local/bin/eyvescloud 不存在"
fi

# ---------------------------------------------------------------------------
# 步骤 4：API 功能自检
# ---------------------------------------------------------------------------
hdr "步骤 4：API 功能自检"
c="$(http_code "$BASE/")"
[ "$c" = "200" ] && record PASS "GET / 返回 200" || record FAIL "GET / 返回 200" "code=$c"

c="$(http_code "$BASE/api/version")"
[ "$c" = "200" ] && record PASS "GET /api/version 返回 200" || record FAIL "GET /api/version 返回 200" "code=$c"

c="$(http_code "$BASE/api/health")"
[ "$c" = "200" ] && record PASS "GET /api/health 返回 200" || record FAIL "GET /api/health 返回 200" "code=$c"

# 登录：优先首启凭据文件，其次回退环境变量
ADMIN_USER=""; ADMIN_PASS=""
if [ -f "$CREDS_FILE" ]; then
    ADMIN_USER="$(sed -n 's/^Username: //p' "$CREDS_FILE" | head -1)"
    ADMIN_PASS="$(sed -n 's/^Password: //p' "$CREDS_FILE" | head -1)"
fi
ADMIN_USER="${ADMIN_USER:-${EYVESCLOUD_E2E_ADMIN_USER:-}}"
ADMIN_PASS="${ADMIN_PASS:-${EYVESCLOUD_E2E_ADMIN_PASS:-}}"

TOKEN=""
if [ -n "$ADMIN_USER" ] && [ -n "$ADMIN_PASS" ]; then
    login_body="$(printf '{"username":"%s","password":"%s"}' "$ADMIN_USER" "$ADMIN_PASS")"
    resp="$(http_body "$BASE/api/login" -X POST -H 'Content-Type: application/json' -d "$login_body")"
    TOKEN="$(printf '%s' "$resp" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p' | head -1)"
    if [ -n "$TOKEN" ]; then
        record PASS "管理员登录取得 token"
    else
        record FAIL "管理员登录取得 token" "响应：$(printf '%s' "$resp" | head -c 200)"
    fi
else
    record SKIP "管理员登录取得 token" "无首启凭据文件，且未提供 EYVESCLOUD_E2E_ADMIN_USER/PASS"
fi

if [ -n "$TOKEN" ]; then
    AUTH=(-H "Authorization: Bearer $TOKEN")
    for ep in "/api/dashboard" "/api/containers" "/api/nodes" "/api/sub-users" "/api/security-groups" "/api/health/detail"; do
        c="$(http_code "$BASE$ep" "${AUTH[@]}")"
        [ "$c" = "200" ] && record PASS "GET $ep 返回 200" || record FAIL "GET $ep 返回 200" "code=$c"
    done

    # 写操作自检：创建安全组 → 删除（端到端验证配置写入路径）
    sg_resp="$(http_body "$BASE/api/security-groups" -X POST "${AUTH[@]}" \
        -H 'Content-Type: application/json' -d '{"name":"e2e-canary","tenant_id":"e2e"}')"
    sg_id="$(printf '%s' "$sg_resp" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)"
    if [ -n "$sg_id" ]; then
        record PASS "写操作：创建安全组"
        c="$(http_code "$BASE/api/security-groups/$sg_id" -X DELETE "${AUTH[@]}")"
        case "$c" in 200|204) record PASS "写操作：删除安全组";; *) record FAIL "写操作：删除安全组" "code=$c";; esac
    else
        record FAIL "写操作：创建安全组" "响应：$(printf '%s' "$sg_resp" | head -c 200)"
    fi
fi

# 配置库完好性
if [ -f "$DATA_DIR/config.db" ]; then
    if have python3; then
        ic="$(python3 - "$DATA_DIR/config.db" <<'PY' 2>/dev/null || true
import sqlite3, sys
try:
    c = sqlite3.connect(sys.argv[1])
    print(c.execute("PRAGMA integrity_check").fetchone()[0])
except Exception as e:
    print("error: %s" % e)
PY
)"
        [ "$ic" = "ok" ] && record PASS "config.db integrity_check=ok" || record FAIL "config.db integrity_check=ok" "$ic"
    else
        record SKIP "config.db integrity_check" "无 python3"
    fi
    cp -f "$DATA_DIR/config.db" "$REPORT_DIR/config.db.copy" 2>/dev/null || true
else
    record FAIL "config.db 已生成" "$DATA_DIR/config.db 不存在"
fi

# 首启凭据文件应存在（尚未登录过则保留；已登录后可能被删）
if [ -f "$CREDS_FILE" ]; then
    cp -f "$CREDS_FILE" "$REPORT_DIR/initial-admin-credentials.txt" 2>/dev/null || true
    log "首启凭据文件存在（安装侧正常）"
fi

# ---------------------------------------------------------------------------
# 步骤 5：卸载
# ---------------------------------------------------------------------------
if [ "${EYVESCLOUD_E2E_KEEP:-0}" = "1" ]; then
    hdr "步骤 5：卸载（已按 KEEP=1 跳过）"
    record SKIP "卸载" "EYVESCLOUD_E2E_KEEP=1"
else
    hdr "步骤 5：卸载"
    if EYVESCLOUD_LOG_FILE="$REPORT_DIR/uninstall-script.log" EYVESCLOUD_UNINSTALL_CONFIRM=1 \
        bash "$INSTALL_SH" uninstall >"$REPORT_DIR/uninstall.log" 2>&1 </dev/null; then
        record PASS "卸载脚本退出码 0"
    else
        record FAIL "卸载脚本退出码 0" "rc=$?，见 uninstall.log"
    fi
    tail -n 5 "$REPORT_DIR/uninstall.log" 2>/dev/null | sed 's/^/    uninstall> /' | tee -a "$MAIN_LOG" >/dev/null

    # 服务不应再可管理
    if have systemctl; then
        systemctl is-active --quiet eyvescloud 2>/dev/null && record FAIL "服务已停止" "仍 active" || record PASS "服务已停止"
        if systemctl list-unit-files 2>/dev/null | grep -q '^eyvescloud'; then
            record FAIL "无 eyvescloud 单元残留" "$(systemctl list-unit-files | grep '^eyvescloud' | tr '\n' ' ')"
        else
            record PASS "无 eyvescloud 单元残留"
        fi
    fi

    # 敏感路径应全部清除
    while IFS= read -r p; do
        [ -n "$p" ] || continue
        if [ -e "$p" ] || [ -L "$p" ]; then
            record FAIL "无残留：$p" "仍存在"
        else
            record PASS "无残留：$p"
        fi
    done <<EOF
$LEFTOVER_PATHS
EOF

    # 端口应释放
    still_busy=0
    if have ss && ss -ltn 2>/dev/null | grep -q ":${PANEL_PORT}[[:space:]]"; then still_busy=1; fi
    [ "$still_busy" = "0" ] && record PASS "端口 ${PANEL_PORT} 已释放" || record FAIL "端口 ${PANEL_PORT} 已释放" "仍被占用"
fi

# ---------------------------------------------------------------------------
# 步骤 5.5：安装模式覆盖（controller-only / agent-only）
#   仅当 EYVESCLOUD_E2E_MODES=1 时执行；在主流程卸载后、同一台机上依次验证。
#   目的是确认「模式选择」这条分支不会装错服务、不会互相污染。
# ---------------------------------------------------------------------------
uninstall_quiet() {
    EYVESCLOUD_LOG_FILE="$REPORT_DIR/uninstall-modes.log" EYVESCLOUD_UNINSTALL_CONFIRM=1 \
        bash "$INSTALL_SH" uninstall >/dev/null 2>&1 </dev/null || true
}

if [ "${EYVESCLOUD_E2E_MODES:-0}" = "1" ] && [ "${EYVESCLOUD_E2E_KEEP:-0}" != "1" ]; then
    hdr "步骤 5.5：安装模式覆盖"
    PANEL_UNIT="/etc/systemd/system/eyvescloud.service"
    AGENT_UNIT="/etc/systemd/system/eyvescloud-agent.service"

    # --- controller-only ---
    if ( cd "$INSTALL_CWD" && EYVESCLOUD_LOG_FILE="$REPORT_DIR/mode-controller-script.log" \
            EYVESCLOUD_VERSION="$VER" EYVESCLOUD_INSTALL_MODE=controller \
            bash "$INSTALL_SH_RUN" install ) >"$REPORT_DIR/mode-controller.log" 2>&1 </dev/null; then
        record PASS "模式 controller：安装退出码 0"
        [ -f "$PANEL_UNIT" ] && record PASS "模式 controller：面板单元已安装" \
                             || record FAIL "模式 controller：面板单元已安装" "$PANEL_UNIT 不存在"
        if [ -e "$AGENT_UNIT" ] || [ -e "${AGENT_UNIT}.disabled" ]; then
            record FAIL "模式 controller：未安装 agent 单元" "存在 $AGENT_UNIT"
        else
            record PASS "模式 controller：未安装 agent 单元"
        fi
        cr=0
        for _ in $(seq 1 30); do
            if [ "$(http_code "$BASE/api/health")" = "200" ]; then cr=1; break; fi
            if have systemctl && systemctl is-active --quiet eyvescloud 2>/dev/null; then cr=1; break; fi
            sleep 2
        done
        [ "$cr" = "1" ] && record PASS "模式 controller：服务/端口就绪" \
                        || record FAIL "模式 controller：服务/端口就绪"
    else
        record FAIL "模式 controller：安装退出码 0" "rc=$?，见 mode-controller.log"
    fi
    uninstall_quiet

    # --- agent-only ---
    # 主控地址用 TEST-NET-2（不可路由）：只为验证单元落盘与启动分支，
    # 不要求真的能连上主控；也避免命中「回环 agent 自愈停用」逻辑。
    if ( cd "$INSTALL_CWD" && EYVESCLOUD_LOG_FILE="$REPORT_DIR/mode-agent-script.log" \
            EYVESCLOUD_VERSION="$VER" EYVESCLOUD_INSTALL_MODE=agent \
            EYVESCLOUD_CONTROLLER="https://198.51.100.10:${PANEL_PORT}" \
            bash "$INSTALL_SH_RUN" install ) >"$REPORT_DIR/mode-agent.log" 2>&1 </dev/null; then
        record PASS "模式 agent：安装退出码 0"
        [ -f "$AGENT_UNIT" ] && record PASS "模式 agent：agent 单元已安装" \
                             || record FAIL "模式 agent：agent 单元已安装" "$AGENT_UNIT 不存在"
        if [ -f "$PANEL_UNIT" ]; then
            record FAIL "模式 agent：未安装面板单元" "存在 $PANEL_UNIT"
        else
            record PASS "模式 agent：未安装面板单元"
        fi
        [ -x /usr/local/bin/eyvescloud ] && record PASS "模式 agent：二进制已安装" \
                                        || record FAIL "模式 agent：二进制已安装"
    else
        record FAIL "模式 agent：安装退出码 0" "rc=$?，见 mode-agent.log"
    fi
    uninstall_quiet

    # 收尾：确认两种模式卸载后同样无残留
    if [ -e "$PANEL_UNIT" ] || [ -e "$AGENT_UNIT" ] || [ -e "${AGENT_UNIT}.disabled" ]; then
        record FAIL "模式覆盖后无单元残留" "仍存在 eyvescloud 单元"
    else
        record PASS "模式覆盖后无单元残留"
    fi
fi

# ---------------------------------------------------------------------------
# 步骤 6：汇总 + 打包
# ---------------------------------------------------------------------------
hdr "步骤 6：汇总"
# 注意：`grep -c` 无匹配时仍会打印 0 但返回非 0，用 `|| true` 兜住退出码，
# 切勿 `|| echo 0` —— 那会把计数变成 "0\n0" 撑坏汇总行。
pass_n=$(grep -c '^PASS' "$RESULTS" 2>/dev/null || true)
warn_n=$(grep -c '^WARN' "$RESULTS" 2>/dev/null || true)
skip_n=$(grep -c '^SKIP' "$RESULTS" 2>/dev/null || true)
fail_n=$(grep -c '^FAIL' "$RESULTS" 2>/dev/null || true)

{
    echo "EyvesCloud 全新系统一键验收报告"
    echo "时间：$(date)"
    echo "版本：${VER:-unknown}"
    echo "报告目录：$REPORT_DIR"
    echo "结果：PASS=$pass_n WARN=$warn_n SKIP=$skip_n FAIL=$fail_n"
    echo
    printf '%-6s %-40s %s\n' STATUS ITEM DETAIL
    awk -F'\t' '{printf "%-6s %-40s %s\n", $1, $2, $3}' "$RESULTS"
} > "$REPORT_DIR/summary.txt"
cat "$REPORT_DIR/summary.txt" | tee -a "$MAIN_LOG"

bundle="${REPORT_DIR}.tgz"
tar -C "$(dirname "$REPORT_DIR")" -czf "$bundle" "$(basename "$REPORT_DIR")" 2>/dev/null || true

echo ""
echo "========================================"
echo "  验收结果：PASS=$pass_n  WARN=$warn_n  SKIP=$skip_n  FAIL=$fail_n"
echo "  报告目录：$REPORT_DIR"
echo "  回传打包：$bundle"
echo "========================================"
[ "$fail_n" = "0" ] && exit 0 || exit 1
