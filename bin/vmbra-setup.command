#!/bin/bash
# 双击运行：一键配置 Vmbra 运行环境（Xcode CLT → Homebrew → Git → Node → VPS → lima → Claude Code）
# 也可在终端执行：./bin/vmbra-setup.command
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SETUP="$ROOT/scripts/vmbra-setup/setup.sh"

if [ ! -x "$SETUP" ]; then
  chmod +x "$SETUP" 2>/dev/null
fi

echo "=========================================="
echo " Vmbra 一键配置环境"
echo "=========================================="
echo

args=()
if [ $# -gt 0 ]; then
  args=("$@")
else
  key=$(osascript <<'EOS'
try
  set r to display dialog "请输入授权码（形如 VMB-XXXX-XXXX-XXXX-XXXX）\n已有配置可留空跳过校验" with title "配置 Vmbra" default answer "" buttons {"取消", "开始配置"} default button "开始配置" with icon note
  return text returned of r
on error number -128
  return ""
end try
EOS
)
  if [ -z "$key" ]; then
    args=(--skip-license)
  else
    args=(--license "$key")
  fi
fi

"$SETUP" "${args[@]}"
code=$?

echo
if [ $code -eq 0 ]; then
  echo "环境已就绪，可以打开 Vmbra 了。"
  osascript -e 'display alert "配置完成" message "Vmbra 环境已就绪，现在可以打开 Vmbra 开启工作模式。" as note' 2>/dev/null
else
  echo "配置未完成，日志见 ~/.vmbra/logs/"
  osascript -e 'display alert "配置未完成" message "请查看本窗口的错误信息，或联系服务商。" as critical' 2>/dev/null
fi

exit $code
