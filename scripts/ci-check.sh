#!/usr/bin/env bash
#
# ci-check.sh —— EyvesCloud 提交前/发版前的一键质量门禁。
#
# 为什么需要它：本项目的几类严重缺陷（数据竞态、配置字段不落库、静默失败）
# 都**不会**被"代码能编译"发现——它们只在运行期、或只在 -race 下才暴露。
# 这些检查此前只靠人工在服务器上敲命令，等于没有防线：修过一次的问题，
# 下一版代码完全可能原样写回来且无人察觉。
#
# 覆盖：
#   1. go vet           —— 静态可疑点
#   2. go test -race    —— 并发正确性（本项目抓到过 5 处真实竞态）
#   3. go test          —— 功能回归
#   4. go build         —— 交叉编译可用性
#   5. 前端 tsc --noEmit —— 类型正确性
#   6. 前端 build        —— 产物可生成
#   7. 关键契约自检      —— 见下方"防退化断言"
#
# 用法：
#   ./scripts/ci-check.sh          # 全量
#   ./scripts/ci-check.sh --fast   # 跳过 -race（本地快速迭代用）
#
# 退出码：0 = 全过；非 0 = 有失败项（CI 应据此阻断合并）。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
FRONTEND="$ROOT/frontend"
FAST=0
CONTRACTS_ONLY=0
for arg in "$@"; do
  case "$arg" in
    --fast)           FAST=1 ;;
    --contracts-only) CONTRACTS_ONLY=1 ;;
  esac
done

FAILED=()
step() { printf '\n\033[1m▶ %s\033[0m\n' "$1"; }
ok()   { printf '  \033[32m✓ %s\033[0m\n' "$1"; }
bad()  { printf '  \033[31m✗ %s\033[0m\n' "$1"; FAILED+=("$1"); }

# ---------------------------------------------------------------------------
# 防退化断言
#
# 每一条都对应一个**真实修过的缺陷**。它们不会被常规测试发现（编译通过、
# 甚至跑起来也没事），所以必须在门禁里显式钉住。
# ---------------------------------------------------------------------------
verify_contracts() {
  step "关键契约自检（防已修缺陷退化）"

  # 1) v2 认证必须保留 CSRF 纵深防御（N-1：v2Auth 曾整棵树没有来源校验）。
  if grep -q 'cookieCSRFGuard' "$BACKEND/internal/api/apiv2_core.go"; then
    ok "v2Auth 保留了 cookieCSRFGuard"
  else
    bad "v2Auth 丢失 cookieCSRFGuard（CSRF 纵深防御退化）"
  fi

  # 2) 节点心跳必须用常量时间比较（N-2：曾用 != 比较 token）。
  if grep -q 'ConstantTimeCompare' "$BACKEND/internal/api/nodes.go"; then
    ok "节点心跳使用常量时间比较"
  else
    bad "节点心跳改用非常量时间比较（时序侧信道回归）"
  fi

  # 3) 登录类限流桶键不得包含攻击者可控的输入串。
  #    曾出现 ip+"|user:"+username、ip+"|code:"+code 等写法，
  #    攻击者轮换变体即可各自拿到全新配额（访问码那条等于无限爆破）。
  local leak
  leak=$(grep -rnE 'rateKey := .*\+ *(req\.(Username|Code|Password)|code)' \
           --include=*.go "$BACKEND/internal/api/" 2>/dev/null | grep -v _test || true)
  if [ -z "$leak" ]; then
    ok "限流桶键不含攻击者可控输入"
  else
    bad "限流桶键含攻击者可控输入："; printf '      %s\n' "$leak"
  fi

  # 4) 安全组配置必须落库（曾完全不落库：重启后配置全丢）。
  if grep -q '"sec_groups"' "$BACKEND/internal/config/store_sqlite.go"; then
    ok "安全组配置已纳入持久化"
  else
    bad "安全组配置未落库（重启后丢失）"
  fi

  # 5) 安全组执行层必须存在（曾只有 Compile，无任何下发）。
  if grep -q 'BuildIptablesCommands' "$BACKEND/internal/secgroup/iptables.go" 2>/dev/null; then
    ok "安全组执行层存在"
  else
    bad "安全组缺少下发实现（规则不会生效）"
  fi

  # 6) 容器列表必须走锁内快照（曾无锁直取全局切片 → 数据竞态）。
  #    注意不能靠"包含某关键词"判断——函数注释里就写着"直接引用全局切片"，
  #    必须精确匹配赋值语句本身。
  local listbody
  listbody=$(sed -n '/func (m \*Manager) ListContainers/,/^}/p' "$BACKEND/internal/lxc/lxc.go")
  if printf '%s' "$listbody" | grep -qE '=\s*config\.AppConfig\.Containers'; then
    bad "容器列表重新直取全局切片（并发竞态回归）"
  elif printf '%s' "$listbody" | grep -q 'config.GetContainers()'; then
    ok "容器列表使用锁内快照"
  else
    bad "容器列表的数据来源不明（既非直取也非 GetContainers）"
  fi

  # 7) 配置落库失败不得静默吞掉（曾有 33 处 `_ = SaveConfig()`）。
  local silent
  silent=$(grep -rnE '_ = (config\.)?[Ss]aveConfig(ToDB)?\(\)' \
             --include=*.go "$BACKEND/internal/" 2>/dev/null | grep -v _test || true)
  if [ -z "$silent" ]; then
    ok "配置落库失败不再被静默吞掉"
  else
    bad "存在被静默吞掉的落库调用："; printf '      %s\n' "$silent"
  fi
}

# ---------------------------------------------------------------------------
# 后端
# ---------------------------------------------------------------------------
run_backend() {
  step "后端：go vet"
  ( cd "$BACKEND" && go vet ./... ) && ok "go vet" || bad "go vet"

  if [ "$FAST" -eq 0 ]; then
    step "后端：go test -race（并发正确性）"
    ( cd "$BACKEND" && go test -race ./internal/... -count=1 ) \
      && ok "go test -race" || bad "go test -race"
  else
    printf '\n\033[33m⊘ 跳过 -race（--fast）\033[0m\n'
  fi

  step "后端：go test"
  ( cd "$BACKEND" && go test ./internal/... -count=1 ) && ok "go test" || bad "go test"

  step "后端：go build"
  ( cd "$BACKEND" && go build ./... ) && ok "go build" || bad "go build"
}

# ---------------------------------------------------------------------------
# 前端
# ---------------------------------------------------------------------------
run_frontend() {
  if [ ! -d "$FRONTEND/node_modules" ]; then
    step "前端：安装依赖"
    ( cd "$FRONTEND" && npm ci ) && ok "npm ci" || { bad "npm ci"; return; }
  fi

  step "前端：tsc --noEmit（类型检查）"
  ( cd "$FRONTEND" && npx tsc --noEmit ) && ok "tsc" || bad "tsc"

  step "前端：build"
  ( cd "$FRONTEND" && npm run build ) && ok "build" || bad "build"
}

# ---------------------------------------------------------------------------
# 主流程
# ---------------------------------------------------------------------------
echo "═══════════════════════════════════════════"
echo "  EyvesCloud CI 门禁$([ "$FAST" -eq 1 ] && echo '（快速模式，跳过 -race）')"
echo "═══════════════════════════════════════════"

verify_contracts
if [ "$CONTRACTS_ONLY" -eq 0 ]; then
  run_backend
  run_frontend
else
  printf '\n\033[33m⊘ 仅契约自检模式：跳过编译与测试\033[0m\n'
fi

echo
echo "═══════════════════════════════════════════"
if [ ${#FAILED[@]} -eq 0 ]; then
  printf '  \033[32m全部通过\033[0m\n'
  echo "═══════════════════════════════════════════"
  exit 0
fi
printf '  \033[31m失败 %d 项：\033[0m\n' "${#FAILED[@]}"
for f in "${FAILED[@]}"; do printf '    - %s\n' "$f"; done
echo "═══════════════════════════════════════════"
exit 1
