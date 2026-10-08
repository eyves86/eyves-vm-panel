#!/bin/bash
# run_hb.sh —— 被控节点心跳入站压测（沙箱化，仅测试机使用）。
# 沙箱：EYVESCLOUD_DATA_DIR 重定向 + 桩 PATH + iptables 挡外网 18996 + nobody 运行。
# CPU 分隔：面板跑 PANEL_CPUS，压测器跑 GEN_CPUS，互不抢核（本机 4 核）。
# 绝不触碰线上面板（8999/18999）。
set -u
NODES=${NODES:-30000}
PERNODE=${PERNODE:-10}
SUBS=${SUBS:-1000}
PANEL_CPUS=${PANEL_CPUS:-0,1}
GEN_CPUS=${GEN_CPUS:-2,3}
PHASES=${PHASES:-"0:128:45 10:128:45 10:256:45 10:512:45"}
BASE=/tmp/eyves-hb
DATA=$BASE/data
HOMEDIR=$BASE/home
STUB=$BASE/stub
PORT=18996
PASS=${LOADPASS:-bench-pass-123}
REPO=${REPO:-/root/eyves-fix}
GOBIN=${GOBIN:-/usr/local/go/bin/go}

rm -rf "$BASE"
mkdir -p "$DATA" "$HOMEDIR" "$STUB"
for t in lxc-info lxc-ls lxc-attach lxc-start lxc-stop virsh; do
  printf '#!/bin/sh\nexit 1\n' > "$STUB/$t"
  chmod +x "$STUB/$t"
done

cd "$REPO/backend" || exit 1
"$GOBIN" build -o "$BASE/eyves" . || exit 1
mkdir -p cmd/_loadseed cmd/_hbgen
cp "$REPO/loadtest-tools/loadseed.go" cmd/_loadseed/main.go
cp "$REPO/loadtest-tools/hbgen.go" cmd/_hbgen/main.go
"$GOBIN" build -o "$BASE/loadseed" ./cmd/_loadseed || exit 1
"$GOBIN" build -o "$BASE/hbgen" ./cmd/_hbgen || exit 1
rm -rf cmd/_loadseed cmd/_hbgen
echo "BUILD-OK"

export EYVESCLOUD_DATA_DIR=$DATA
export HOME=$HOMEDIR
echo "▶ 注入夹具 nodes=$NODES conts_per_node=$PERNODE"
s0=$(date +%s.%N)
"$BASE/loadseed" -nodes "$NODES" -conts-per-node "$PERNODE" \
  -subusers "$SUBS" -port "$PORT" -admin-pass "$PASS" || exit 1
s1=$(date +%s.%N)
echo "SEED-SECS $(awk -v a="$s0" -v b="$s1" 'BEGIN{printf "%.1f", b-a}')"
chmod -R 777 "$BASE"

iptables -I INPUT -p tcp --dport $PORT '!' -i lo -j DROP 2>/dev/null
IPT_SET=1
cleanup() {
  if [ "${KEEP:-0}" = 1 ]; then
    echo "KEEP=1 面板保留：pid=$(cat "$BASE/panel.pid" 2>/dev/null) 数据=$DATA"
    echo "  清理：kill \$(cat $BASE/panel.pid); iptables -D INPUT -p tcp --dport $PORT '!' -i lo -j DROP"
    return
  fi
  if [ -f "$BASE/panel.pid" ]; then
    pid=$(cat "$BASE/panel.pid")
    exe=$(readlink -f "/proc/$pid/exe" 2>/dev/null)
    case "$exe" in
      $BASE/*) kill "$pid" 2>/dev/null ;;
      *) echo "SKIP-KILL pid=$pid exe=$exe 非本次沙箱进程" ;;
    esac
  fi
  if [ "${IPT_SET:-0}" = 1 ]; then
    iptables -D INPUT -p tcp --dport $PORT '!' -i lo -j DROP 2>/dev/null
  fi
}
trap cleanup EXIT

export GOMEMLIMIT=6500MiB
export PATH=$STUB:/usr/local/bin:/usr/bin:/bin
setpriv --reuid=65534 --regid=65534 --clear-groups \
  env EYVESCLOUD_DATA_DIR=$DATA HOME=$HOMEDIR GOMEMLIMIT=$GOMEMLIMIT PATH=$PATH \
  nohup taskset -c "$PANEL_CPUS" "$BASE/eyves" server > "$BASE/panel.log" 2>&1 &
echo $! > "$BASE/panel.pid"
pid=$(cat "$BASE/panel.pid")
NCPU=$(awk -v c="$PANEL_CPUS" 'BEGIN{n=split(c,a,","); print n}')

rss() { awk '/^VmRSS:/{print $2}' "/proc/$pid/status" 2>/dev/null; }
cpu_ticks() { awk '{print $14+$15}' "/proc/$pid/stat" 2>/dev/null; }

ready=0
for i in $(seq 1 900); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/" 2>/dev/null)
  if [ "$code" != "000" ]; then ready=1; break; fi
  sleep 1
done
if [ "$ready" != 1 ]; then
  echo "PANEL-FAILED-TO-START"; tail -20 "$BASE/panel.log"; exit 1
fi
echo "PANEL-READY pid=$pid cpus=$PANEL_CPUS(${NCPU}核) VmRSS=$(rss)kB"

for ph in $PHASES; do
  IFS=':' read -r pc pw pd pint <<< "$ph"
  pint=${pint:-10s}
  echo "=== PHASE conts=$pc workers=$pw dur=${pd}s interval=$pint ==="
  : > "$BASE/rss.out"
  ( while kill -0 "$pid" 2>/dev/null; do r=$(rss); [ -n "$r" ] && echo "$r" >> "$BASE/rss.out"; sleep 0.5; done ) &
  sp=$!
  taskset -c "$GEN_CPUS" "$BASE/hbgen" -base "http://127.0.0.1:$PORT" -nodes "$NODES" \
    -conts "$pc" -workers "$pw" -duration "${pd}s" -interval "$pint" -rss-pid "$pid"
  kill "$sp" 2>/dev/null; wait "$sp" 2>/dev/null
  peak=$(sort -n "$BASE/rss.out" | tail -1)
  echo "PHASE-PEAK conts=$pc workers=$pw interval=$pint VmRSS=${peak}kB"
done
echo "HB-DONE"
