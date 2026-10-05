#!/usr/bin/env bash
#
# release.sh —— 构建 + 打包 + 签名 + 发布到 Codeberg（发版流水线）
#
# 为什么需要它：本仓库远端是 codeberg.org，而 Codeberg（Forgejo）只读取
# .forgejo/workflows/；原先的发版工作流放在 .github/workflows/，且没有 GitHub
# 镜像仓库 —— 等于**从未自动化**，发版一直靠人工手搓。本脚本把发版收敛成一条
# 可复现、可 dry-run 验证的命令，供 CI 与离线人工两条路径共用同一套逻辑。
#
# 用法：
#   EYVESCLOUD_RELEASE_TOKEN=<Codeberg token, 需 write:repository> \
#   EYVESCLOUD_SIGNING_KEY=<ed25519 私钥 32B seed 的 hex> \
#   bash scripts/release.sh v2.2.49
#
# 环境变量：
#   EYVESCLOUD_RELEASE_TOKEN  Codeberg API token（创建 release / 上传资产）。发布时必需。
#   EYVESCLOUD_SIGNING_KEY    ed25519 私钥（32B seed hex）。缺省时**拒绝发布**——避免
#                             发出「无签名 release」导致所有内嵌公钥的面板自升级验签
#                             中止；确需无签名发布用 --allow-unsigned 显式放行。
#   EYVESCLOUD_REPO           默认 codeberg:fenhaolost/eyves-vm-panel
#   EYVESCLOUD_RELEASE_PUBKEY 编译期内嵌的验签公钥（缺省用官方内置值）
#
# 选项：
#   --dry-run         只构建 + 打包 + 签名，不上传（本地验证流水线用）
#   --allow-unsigned  无签名也允许发布（不推荐）
#
# 退出码：0 = 成功；非 0 = 失败。
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REPO="${EYVESCLOUD_REPO:-github:eyves86/eyves-vm-panel}"
PUBKEY="${EYVESCLOUD_RELEASE_PUBKEY:-Xyv7jh+bDXBzIf57+PhyfzCUsfEPOIuyUog/j6burGI=}"
DRY_RUN=0
ALLOW_UNSIGNED=0
TAG=""

for arg in "$@"; do
    case "$arg" in
        --dry-run)        DRY_RUN=1 ;;
        --allow-unsigned) ALLOW_UNSIGNED=1 ;;
        -*) echo "未知选项：$arg" >&2; exit 2 ;;
        *)  TAG="$arg" ;;
    esac
done

log() { printf '\033[1m▶ %s\033[0m\n' "$*"; }
die() { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

[ -n "$TAG" ] || die "缺少版本 tag（例：bash scripts/release.sh v2.2.49）"
VER="${TAG#v}"

# ---- 解析仓库平台 / slug -------------------------------------------------
PLATFORM="codeberg"; SLUG="$REPO"
case "$REPO" in
    *:*) PLATFORM="${REPO%%:*}"; SLUG="${REPO#*:}" ;;
esac
case "$PLATFORM" in
    codeberg|gitea|forgejo) API="https://codeberg.org/api/v1/repos/${SLUG}" ;;
    github)                 API="https://api.github.com/repos/${SLUG}" ;;
    *) die "暂不支持的发布平台：$PLATFORM" ;;
esac
log "发布目标：${PLATFORM}:${SLUG}  版本：${VER}"

# ---- 1. 构建 + 打包（复用 build.sh，单一构建定义）------------------------
log "构建前端 + 后端（amd64/arm64，内嵌 version + 验签公钥）"
EYVESCLOUD_VERSION="$VER" \
EYVESCLOUD_GOARCH=all \
EYVESCLOUD_RELEASE_PUBKEY="$PUBKEY" \
    bash "$ROOT/build.sh" >"${TMPDIR:-/tmp}/eyves-release-build.log" 2>&1 \
    || { tail -n 30 "${TMPDIR:-/tmp}/eyves-release-build.log" >&2; die "构建失败（见 ${TMPDIR:-/tmp}/eyves-release-build.log）"; }
for arch in amd64 arm64; do
    [ -f "$ROOT/dist/eyvescloud-linux-${arch}.tar.gz" ] || die "缺少产物 dist/eyvescloud-linux-${arch}.tar.gz"
done
log "构建完成"

# ---- 2. 生成 SHA256SUMS ---------------------------------------------------
log "生成 SHA256SUMS"
DIST="$ROOT/dist"
( cd "$DIST" && sha256sum eyvescloud-linux-amd64.tar.gz eyvescloud-linux-arm64.tar.gz | sort -k2 > SHA256SUMS )
cat "$DIST/SHA256SUMS"

# ---- 3. 签名（ed25519，minisign 信封）------------------------------------
SIG_OK=0
if [ -n "${EYVESCLOUD_SIGNING_KEY:-}" ]; then
    log "签名 SHA256SUMS（ed25519）"
    ( cd "$ROOT/backend" && go run ./cmd/releasetool sign \
        -key "$(printf '%s' "$EYVESCLOUD_SIGNING_KEY" | tr -d '[:space:]')" \
        -in "$DIST/SHA256SUMS" ) > "$DIST/SHA256SUMS.minisig" \
        || die "签名失败"
    cp "$DIST/SHA256SUMS.minisig" "$DIST/SHA256SUMS.minisign"   # 兼容历史命名
    SIG_OK=1
    log "签名完成"
fi

if [ "$SIG_OK" = "0" ]; then
    if [ "$DRY_RUN" = "1" ] || [ "$ALLOW_UNSIGNED" = "1" ]; then
        printf '\033[33m⊘ 未提供 EYVESCLOUD_SIGNING_KEY，跳过签名\033[0m\n'
    else
        die "未提供 EYVESCLOUD_SIGNING_KEY —— 拒绝发布无签名 release（会破坏面板自升级验签）。\n  请提供私钥，或用 --allow-unsigned 显式放行。"
    fi
fi

# ---- 4. 发布 -------------------------------------------------------------
if [ "$DRY_RUN" = "1" ]; then
    log "dry-run：跳过上传。产物已就绪："
    ls -la "$DIST"/eyvescloud-linux-*.tar.gz "$DIST"/SHA256SUMS*
    exit 0
fi

[ -n "${EYVESCLOUD_RELEASE_TOKEN:-}" ] || die "发布需要 EYVESCLOUD_RELEASE_TOKEN"

log "创建/复用 release ${TAG}"
curl -fsSL -X POST "$API/releases" \
    -H "Authorization: token ${EYVESCLOUD_RELEASE_TOKEN}" \
    -H "Content-Type: application/json" \
    -d "$(printf '{"tag_name":"%s","name":"%s","draft":false,"prerelease":false}' "$TAG" "$TAG")" \
    >/dev/null 2>&1 || true

rel_id="$(curl -fsSL "$API/releases/tags/${TAG}" -H "Authorization: token ${EYVESCLOUD_RELEASE_TOKEN}" \
    | grep -o '"id":[0-9]*' | head -n 1 | grep -oE '[0-9]+')"
[ -n "$rel_id" ] || die "无法获取 release id（tag 是否已推送？）"
log "release id=${rel_id}"

upload() {
    f="$1"
    [ -f "$f" ] || return 0
    log "上传 $(basename "$f")"
    curl -fsSL -X POST "$API/releases/${rel_id}/assets?name=$(basename "$f")" \
        -H "Authorization: token ${EYVESCLOUD_RELEASE_TOKEN}" \
        -H "Content-Type: application/octet-stream" \
        --data-binary "@$f" >/dev/null || die "上传失败：$f"
}
upload "$DIST/eyvescloud-linux-amd64.tar.gz"
upload "$DIST/eyvescloud-linux-arm64.tar.gz"
upload "$DIST/SHA256SUMS"
upload "$DIST/SHA256SUMS.minisig"
upload "$DIST/SHA256SUMS.minisign"

log "发布完成：${TAG}"
