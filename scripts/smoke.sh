#!/usr/bin/env bash
# 管理服务冒烟测试：启动临时实例，逐项验证核心接口后清理。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PORT="${SMOKE_PORT:-7791}"
DATA="$(mktemp -d /tmp/vmbra-smoke.XXXXXX)"
LOG="$DATA/daemon.log"
BIN="$DATA/vmbrad"
PASS=0
FAIL=0

cleanup() {
  if [[ -n "${PID:-}" ]] && kill -0 "$PID" 2>/dev/null; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  if [[ "${FAIL:-0}" -gt 0 ]]; then
    printf '保留现场：%s\n' "$DATA"
    return
  fi
  rm -rf "$DATA"
}
trap cleanup EXIT

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()  { PASS=$((PASS + 1)); printf '  \033[32m✓\033[0m %s\n' "$*"; }
bad() { FAIL=$((FAIL + 1)); printf '  \033[31m✗\033[0m %s\n' "$*"; }

check() { # check <描述> <实际> <期望子串>
  if [[ "$2" == *"$3"* ]]; then ok "$1"; else bad "$1（实际：${2:0:160}）"; fi
}

say "编译"
(cd "$ROOT" && go build -o "$BIN" ./cmd/vmbrad)
ok "vmbrad 编译完成"

say "启动临时实例（端口 ${PORT}）"
"$BIN" --port "$PORT" --data-dir "$DATA/state" >"$LOG" 2>&1 &
PID=$!
for _ in $(seq 1 60); do
  [[ -f "$DATA/state/token" ]] && curl -fsS -o /dev/null "http://127.0.0.1:$PORT/" 2>/dev/null && break
  sleep 0.2
done
TOKEN="$(cat "$DATA/state/token")"
H=(-H "X-Vmbra-Token: $TOKEN")
BASE="http://127.0.0.1:$PORT"
api() { curl -sS "${H[@]}" "$@"; }
ok "服务已就绪"

say "鉴权"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/status")
check "无令牌访问被拒绝" "$code" "401"
body=$(curl -s -H 'Host: evil.example.com' -o /dev/null -w '%{http_code}' "$BASE/api/status")
check "非回环 Host 被拒绝" "$body" "403"

say "工作站"
check "启动工作站" "$(api -X POST "$BASE/api/workstation/start")" '"state":"running"'
check "策略隔离生效" "$(api "$BASE/api/status")" '"sandbox":true'
check "概览包含设置快照" "$(api "$BASE/api/status")" '"settings"'

say "端口转发"
# 用 printf 生成 JSON，避免 bash 花括号展开把请求体拆开
fwd_body() { printf '{"label":"%s","hostPort":%d,"guestPort":%d,"proto":"%s"}' "$1" "$2" "$3" "$4"; }
check "拒绝 UDP 规则" "$(api -X POST "${H[@]}" -H 'Content-Type: application/json' \
  -d "$(fwd_body udp 17802 "$PORT" udp)" "$BASE/api/network/forwards")" '仅支持 TCP'
FWD=$(api -X POST "${H[@]}" -H 'Content-Type: application/json' \
  -d "$(fwd_body 自测 17801 "$PORT" tcp)" "$BASE/api/network/forwards")
FID=$(echo "$FWD" | python3 -c 'import sys,json;print(json.load(sys.stdin)["id"])')
check "转发已监听" "$(api "$BASE/api/network")" '"listening":true'
check "转发链路可用" "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:17801/api/status)" "401"
check "链路自检通过" "$(api -X POST "$BASE/api/network/forwards/$FID/test")" '"guestOk":true'
check "停用后释放端口" "$(api -X PATCH "${H[@]}" -H 'Content-Type: application/json' -d '{"enabled":false}' \
  "$BASE/api/network/forwards/$FID")" '"enabled":false'
code=$(curl -s -o /dev/null -m 2 -w '%{http_code}' http://127.0.0.1:17801/api/status || true)
check "端口已关闭" "$code" "000"

say "文件"
api -X PUT "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"path":"projects/smoke.txt","content":"smoke"}' "$BASE/api/files/content" >/dev/null
check "写入并读回" "$(api "$BASE/api/files/content?path=projects/smoke.txt")" '"content":"smoke"'
code=$(api -o /dev/null -w '%{http_code}' "$BASE/api/files/content?path=../../etc/passwd")
check "路径穿越被拒绝" "$code" "400"

say "任务与快照"
TASK=$(api -X POST "${H[@]}" -H 'Content-Type: application/json' -d '{"name":"smoke"}' \
  "$BASE/api/maintenance/snapshots" | python3 -c 'import sys,json;print(json.load(sys.stdin)["taskId"])')
sleep 1.2
check "快照任务完成" "$(api "$BASE/api/tasks/$TASK")" '"status":"succeeded"'
check "快照已记录" "$(api "$BASE/api/maintenance")" '"name":"smoke"'

say "导出工作区"
EXPORT="$DATA/export.tar.gz"
code=$(api -o "$EXPORT" -w '%{http_code}' "$BASE/api/maintenance/export")
check "导出接口可用" "$code" "200"
check "导出为 gzip 归档" "$(file -b "$EXPORT")" "gzip"
check "归档包含工作区文件" "$(tar -tzf "$EXPORT")" "projects/smoke.txt"

say "应用包解包（.deb）"
DEBDIR="$DATA/debdemo"
mkdir -p "$DEBDIR"
# 用 Python 直接拼一个带越界条目的最小 deb，避免依赖 macOS ar 的非标准行为。
python3 - "$DEBDIR/demo.deb" <<'PY'
import io, sys, tarfile

def tgz(entries):
    buf = io.BytesIO()
    with tarfile.open(fileobj=buf, mode="w:gz") as tf:
        for name, body in entries:
            info = tarfile.TarInfo(name)
            info.size = len(body)
            info.mode = 0o644
            tf.addfile(info, io.BytesIO(body))
    return buf.getvalue()

control = tgz([("./control", b"Package: demo\nVersion: 1.0\nArchitecture: arm64\n")])
data = tgz([
    ("./usr/share/demo/readme", b"demo-file\n"),
    ("../evil.txt", b"escaped\n"),
])
out = open(sys.argv[1], "wb")
out.write(b"!<arch>\n")

def member(name, payload):
    header = ("%-16s%-12d%-6d%-6d%-8s%-10d`\n" % (name, 0, 0, 0, "100644", len(payload))).encode()
    out.write(header)
    out.write(payload)
    if len(payload) % 2:
        out.write(b"\n")

member("debian-binary", b"2.0\n")
member("control.tar.gz", control)
member("data.tar.gz", data)
out.close()
PY
DEBTASK=$(api -X POST -F "file=@$DEBDIR/demo.deb" "$BASE/api/apps/deb" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["taskId"])')
for _ in $(seq 1 30); do
  DEBLOG=$(api "$BASE/api/tasks/$DEBTASK")
  [[ "$DEBLOG" == *'"status":"succeeded"'* || "$DEBLOG" == *'"status":"failed"'* ]] && break
  sleep 0.2
done
check "解包任务完成" "$DEBLOG" '"status":"succeeded"'
check "包内容已展开" "$(cat "$DATA/state/workstation/workspace/packages/demo/root/usr/share/demo/readme" 2>/dev/null)" "demo-file"
check "主控信息已解析" "$DEBLOG" "Package: demo"
if [[ -e "$DATA/state/workstation/workspace/packages/demo/evil.txt" ]]; then
  bad "越界条目被丢弃"
else
  ok "越界条目被丢弃"
fi

say "系统语言文字"
check "语言环境为 UTF-8" "$(api "$BASE/api/system")" '"encoding":"UTF-8"'
check "应用中文语言" "$(api -X PATCH "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"lang":"zh_CN.UTF-8","timezone":"Asia/Shanghai"}' "$BASE/api/system/locale")" '"lang":"zh_CN.UTF-8"'
check "环境文件已落地" "$(cat "$DATA/state/workstation/home/.vmbra.env")" "export LANG='zh_CN.UTF-8'"
check "拒绝非 UTF-8 编码" "$(api -X PATCH "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"encoding":"GBK"}' "$BASE/api/system/locale")" '仅支持 UTF-8'

say "DNS"
check "转发器运行中" "$(api "$BASE/api/system")" '"running":true'
check "自定义上游生效" "$(api -X PATCH "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"mode":"custom","servers":["223.5.5.5"]}' "$BASE/api/system/dns")" '223.5.5.5:53'
check "解析测试含本地转发器" "$(api -X POST "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"name":"example.com","timeoutSec":3}' "$BASE/api/system/dns/test")" '"kind":"proxy"'

say "运行脚本（默认允许）"
api -X PUT "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"path":"scripts/smoke.sh","content":"echo SMOKE_RUN_OK; echo $LANG"}' "$BASE/api/files/content" >/dev/null
RUN=$(api -X POST "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"path":"scripts/smoke.sh","timeoutSec":30}' "$BASE/api/apps/run" \
  | python3 -c 'import sys,json;print(json.load(sys.stdin)["taskId"])')
sleep 1.2
RUNLOG=$(api "$BASE/api/tasks/$RUN")
check "脚本默认允许执行" "$RUNLOG" '"status":"succeeded"'
check "脚本输出与语言环境" "$RUNLOG" 'SMOKE_RUN_OK'

say "操控 Mac（默认允许，无需确认）"
check "默认允许执行命令" "$(api "$BASE/api/settings")" '"allowMacRunCommand":true'
check "命令可执行" "$(api -X POST "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"command":"echo SMOKE_MAC","timeoutSec":10}' "$BASE/api/mac/run")" 'SMOKE_MAC'
check "审计不记录命令内容" "$(api "$BASE/api/mac/audit")" '"digest"'

say "受控浏览器"
BROWSER=$(api "$BASE/api/browser")
check "读取浏览器配置" "$BROWSER" '"lang":"en-US"'
check "拒绝非法时区" "$(api -X PUT "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"timeZone":"../../etc/passwd"}' "$BASE/api/browser")" '时区不合法'
if [[ "$BROWSER" == *'"available":true'* ]]; then
  check "语言与时区自检通过" "$(api -X POST "$BASE/api/browser/verify")" '"ok":true'
fi

say "VPS 出入口"
check "读取配置" "$(api "$BASE/api/vps")" '"socksPort":1080'
check "生成专用密钥" "$(api -X POST "$BASE/api/vps/key")" 'ssh-ed25519'
check "保存 VPS 配置" "$(api -X PUT "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"host":"203.0.113.10","port":22,"user":"root","socksPort":1080}' "$BASE/api/vps")" '"host":"203.0.113.10"'
check "隧道未运行时如实上报" "$(api "$BASE/api/vps")" '"running":false'
check "拒绝非法主机" "$(api -X PUT "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"host":"bad host"}' "$BASE/api/vps")" '不合法'
check "环境跟随 VPS 地区" "$(api -X PATCH "${H[@]}" -H 'Content-Type: application/json' \
  -d '{"followVps":true}' "$BASE/api/system/locale")" '"timezone":"America/Los_Angeles"'
check "终端时区已切换" "$(api "$BASE/api/system")" '"key":"TZ","value":"America/Los_Angeles"'
check "语言已切换" "$(api "$BASE/api/system")" '"lang":"en_US.UTF-8"'

say "诊断"
check "自检无失败项" "$(api "$BASE/api/diagnostics" | python3 -c 'import sys,json;print(json.load(sys.stdin)["summary"]["fail"])')" "0"

say "停止工作站"
check "停止工作站" "$(api -X POST "$BASE/api/workstation/stop")" '"state":"stopped"'

printf '\n通过 %d 项，失败 %d 项\n' "$PASS" "$FAIL"
[[ "$FAIL" -eq 0 ]] || { echo "日志：$LOG"; exit 1; }
