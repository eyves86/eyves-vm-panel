#!/usr/bin/env bash
#
# load-baseline.sh —— EyvesCloud 规模压测基线（#91）。
#
# 为什么需要它：项目对外只能给「某机器、某规模下的实测延迟」，不能给拍脑袋的容量
# 数字（见项目记忆里的容量口径约定）。本脚本把关键写路径的单次成本、写入放大量、
# 批处理合并率一次性跑出来，打印成固定格式并归档，便于跨版本 / 跨机器对比。
#
# 覆盖：
#   1. config 包基线（TestLoadBaseline）：全量扫描 / 精确保存 / 心跳式保存 / 固定下限 /
#      批处理合并，均含「单核≈N 次/秒」换算。
#   2. api 包心跳基准（BenchmarkNodeHeartbeatAtScale）：验证接入路径 O(m) 与 N 无关。
#
# 用法（在目标机器上，需 Go 工具链）：
#   ./scripts/load-baseline.sh
#   EYVES_LB_CONTAINERS=100000 EYVES_LB_NODES=30000 EYVES_LB_SUBUSERS=100000 \
#     ./scripts/load-baseline.sh
#
# 可选环境变量（透传给 Go 测试）：
#   EYVES_LB_CONTAINERS  容器数（默认 20000）
#   EYVES_LB_NODES       节点数（默认 30000）
#   EYVES_LB_SUBUSERS    子用户数（默认 20000）
#   EYVES_LB_ROUNDS      每项取最小值的轮数（默认 7）
#   EYVES_LB_BENCH_N     心跳基准的容器总量（逗号分隔，默认 2000,20000,200000）
#   EYVES_LB_OUT         报告输出目录（默认 ./load-baseline-out）
#
# 退出码：0 = 跑完并产出报告；非 0 = 环境/编译失败。

set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BACKEND="$ROOT/backend"
OUT="${EYVES_LB_OUT:-$ROOT/load-baseline-out}"
TS="$(date +%Y%m%d-%H%M%S)"
REPORT="$OUT/load-baseline-$TS.txt"

: "${EYVES_LB_CONTAINERS:=20000}"
: "${EYVES_LB_NODES:=30000}"
: "${EYVES_LB_SUBUSERS:=20000}"
: "${EYVES_LB_ROUNDS:=7}"
: "${EYVES_LB_BENCH_N:=2000,20000,200000}"

mkdir -p "$OUT"

GO="${GO:-go}"
if ! command -v "$GO" >/dev/null 2>&1 && [ -x /usr/local/go/bin/go ]; then
  GO=/usr/local/go/bin/go
fi
if ! command -v "$GO" >/dev/null 2>&1; then
  echo "未找到 Go 工具链（设置 GO=/path/to/go）" >&2
  exit 2
fi

{
  echo "EyvesCloud 规模压测基线"
  echo "时间: $TS"
  echo "主机: $(uname -srm 2>/dev/null || echo unknown)  内核: $(uname -r 2>/dev/null)"
  echo "CPU: $(nproc 2>/dev/null || echo '?') 核"
  echo "Go: $("$GO" version)"
  echo "规模: 容器=$EYVES_LB_CONTAINERS 节点=$EYVES_LB_NODES 子用户=$EYVES_LB_SUBUSERS 轮数=$EYVES_LB_ROUNDS"
  echo "----------------------------------------------------------------"
} | tee "$REPORT"

echo "▶ [1/2] config 包基线（TestLoadBaseline）"
( cd "$BACKEND" && EYVES_LOAD_BASELINE=1 \
    EYVES_LB_CONTAINERS="$EYVES_LB_CONTAINERS" \
    EYVES_LB_NODES="$EYVES_LB_NODES" \
    EYVES_LB_SUBUSERS="$EYVES_LB_SUBUSERS" \
    EYVES_LB_ROUNDS="$EYVES_LB_ROUNDS" \
    "$GO" test -run TestLoadBaseline -v -timeout 40m ./internal/config/ 2>&1 ) \
  | grep -E "LOADBASELINE\||^--- (PASS|FAIL)|^(ok|FAIL|PASS)" | tee -a "$REPORT"

echo "▶ [2/2] api 包心跳基准（BenchmarkNodeHeartbeatAtScale）"
# 基准规模通过多个 -bench 子项名筛选或直接全跑；这里按 N 列表逐个跑并拼接。
IFS=',' read -r -a NS <<< "$EYVES_LB_BENCH_N"
for n in "${NS[@]}"; do
  n="$(echo "$n" | tr -d ' ')"
  echo "  · N=$n"
  ( cd "$BACKEND" && "$GO" test -bench="BenchmarkNodeHeartbeatAtScale/N=$n$" \
      -benchtime=2000x -run='^$' ./internal/api/ 2>&1 ) \
    | grep -E "BenchmarkNodeHeartbeat|ns/op|allocs/op" | tee -a "$REPORT"
done

echo "----------------------------------------------------------------" | tee -a "$REPORT"
echo "报告已写入: $REPORT"
echo
echo "口径说明：ns/op 与 N 无关即证明接入路径为 O(本节点容器数)；" 
echo "「单核≈N 次/秒」是单次落库耗时的倒数，仅为单核上限，不含并发与 IO 争用。"
