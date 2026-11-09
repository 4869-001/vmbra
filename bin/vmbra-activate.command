#!/bin/bash
# 双击运行：输入授权码，调用本机 anywhered 的授权接口完成激活
# 需要 anywhered 已在运行（open dist/AnyWhere.app 或 make run）
set -u

BASE="${ANYWHERE_BASE:-http://127.0.0.1:7788}"
TOKEN_FILE="${ANYWHERE_DATA_DIR:-$HOME/.anywhere}/token"

if ! curl -fsS -o /dev/null --max-time 3 "$BASE/api/status"; then
  osascript -e 'display alert "Vmbra 未运行" message "请先打开 Vmbra 桌面程序或执行 make run，再重试激活。" as critical' 2>/dev/null
  exit 1
fi

if [ ! -r "$TOKEN_FILE" ]; then
  echo "找不到访问令牌：$TOKEN_FILE"
  exit 1
fi
TOKEN="$(cat "$TOKEN_FILE")"

key=$(osascript <<'EOS'
try
  set r to display dialog "请输入授权码（形如 VMB-XXXX-XXXX-XXXX-XXXX）" with title "激活 Vmbra" default answer "" buttons {"取消", "激活"} default button "激活" with icon note
  return text returned of r
on error number -128
  return ""
end try
EOS
)

if [ -z "$key" ]; then
  exit 0
fi

echo "正在向授权中心校验…"
resp=$(curl -fsS -X POST "$BASE/api/license/activate" \
  -H "X-Anywhere-Token: $TOKEN" \
  -H "Content-Type: application/json" \
  --data "$(printf '{"key":"%s"}' "$key")" 2>&1)
code=$?

if [ $code -ne 0 ]; then
  echo "$resp"
  osascript -e "display alert \"激活失败\" message \"$(printf '%s' "$resp" | tr '\n' ' ' | cut -c1-200)\" as critical" 2>/dev/null
  exit 1
fi

echo "$resp"
plan=$(printf '%s' "$resp" | sed -n 's/.*"plan":"\([^"]*\)".*/\1/p')
osascript -e "display alert \"激活成功\" message \"套餐：${plan:-已激活}\n\n接下来可运行「配置环境」完成安装。\" as note" 2>/dev/null
