#!/bin/sh
# 沙箱测试面板的重启脚本。
# 面板进程名特意用 adapter-target-server —— 并发会话的 `pkill -f eyvescloud`
# 会误杀同名进程，这个坑踩过。
#
# 用法：sh tests/panel.sh [seed|restart|stop|status]
set -e

HOME_DIR=/tmp/eyves-adapter/home
DATA_DIR=/tmp/eyves-adapter/data
RUN_DIR=/tmp/evtest
BIN="$RUN_DIR/adapter-target-server"
PORT=8998

stop() {
  pkill -f adapter-target-server 2>/dev/null || true
  sleep 1
}

status() {
  code=$(curl -s -m 4 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/containers" || true)
  if [ "$code" = "401" ] || [ "$code" = "200" ]; then
    echo "面板在线（HTTP $code）"
    exit 0
  fi
  echo "面板离线（HTTP $code）"
  exit 1
}

start() {
  mkdir -p "$HOME_DIR" "$DATA_DIR"
  ( cd "$RUN_DIR" && HOME="$HOME_DIR" EYVESCLOUD_DATA_DIR="$DATA_DIR" \
      setsid ./adapter-target-server server > "$RUN_DIR/server.log" 2>&1 & )
  i=0
  while [ $i -lt 20 ]; do
    sleep 1
    code=$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/api/containers" || true)
    if [ "$code" = "401" ] || [ "$code" = "200" ]; then
      echo "面板已启动（HTTP $code）"
      return 0
    fi
    i=$((i + 1))
  done
  echo "面板启动失败，日志尾部："
  tail -5 "$RUN_DIR/server.log"
  return 1
}

case "${1:-restart}" in
  stop)    stop; echo "已停止" ;;
  status)  status ;;
  seed)    stop; sh "$(dirname "$0")/seed.sh" >/dev/null; start ;;
  restart) stop; start ;;
  *)       echo "用法: $0 [seed|restart|stop|status]"; exit 2 ;;
esac
