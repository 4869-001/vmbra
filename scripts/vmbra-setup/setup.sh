#!/usr/bin/env bash
# Vmbra 客户端一键环境配置
# 用法：
#   ./setup.sh --license VMB-XXXX-XXXX-XXXX-XXXX
#   ./setup.sh --license ... --json          # 供 vmbra 控制台消费的 NDJSON 进度
#   ./setup.sh --license ... --dry-run       # 只打印将执行的命令
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/common.sh
. "$SCRIPT_DIR/lib/common.sh"
# shellcheck source=lib/license.sh
. "$SCRIPT_DIR/lib/license.sh"
# shellcheck source=lib/steps.sh
. "$SCRIPT_DIR/lib/steps.sh"

LICENSE_KEY="${VMBRA_LICENSE:-}"
LICENSE_SERVER="${VMBRA_LICENSE_SERVER:-https://license.vmbra.app}"
VPS_OVERRIDE=""
CREATE_VM="${VMBRA_CREATE_VM:-0}"
SKIP_VM="${VMBRA_SKIP_VM:-0}"
SKIP_LICENSE="${VMBRA_SKIP_LICENSE:-0}"
TOOLS="${VMBRA_TOOLS:-chrome,node,git,claude}"
ASSUME_YES=0
VM_NAME=""

# 是否勾选了某个安装项（未勾选的阶段仍然输出 skip，保证进度分母恒定）
tool_wanted() {
  case ",${TOOLS}," in
    *",$1,"*) return 0 ;;
  esac
  return 1
}

usage() {
  cat <<'EOF'
Vmbra 客户端一键环境配置

参数：
  --license <KEY>     授权码（也可用环境变量 VMBRA_LICENSE）
  --server <URL>      授权中心地址，默认 https://license.vmbra.app
  --vps <host:port>   手动指定出口节点，覆盖授权中心下发的地址
  --tools <a,b,c>     只安装勾选的工具（chrome,node,git,claude,vm）
  --vm                包含 Linux 虚拟机终端（lima，会下载约 1GB 镜像）
  --create-vm         若本地没有 lima 实例则自动创建
  --skip-vm           完全跳过虚拟机相关步骤
  --skip-license      跳过授权校验（由客户端主程序已完成校验时使用）
  --json              以 NDJSON 输出进度事件（供控制台消费）
  --dry-run           只打印将执行的命令，不做任何修改
  -y, --yes           跳过交互确认
  -h, --help          显示帮助

环境变量：
  VMBRA_HOME          数据目录，默认 ~/.vmbra
  VMBRA_NODE_VERSION  指定 Node 版本，默认 22.11.0
  VMBRA_TOOLS         等价 --tools
  VMBRA_SKIP_VM=1     等价 --skip-vm
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --license) LICENSE_KEY="${2:-}"; shift 2 ;;
    --server)  LICENSE_SERVER="${2:-}"; shift 2 ;;
    --vps)     VPS_OVERRIDE="${2:-}"; shift 2 ;;
    --tools)   TOOLS="${2:-}"; shift 2 ;;
    --vm)      CREATE_VM=1; TOOLS="$TOOLS,vm"; shift ;;
    --create-vm) CREATE_VM=1; shift ;;
    --skip-vm)   SKIP_VM=1; shift ;;
    --skip-license) SKIP_LICENSE=1; shift ;;
    --json)    JSON_MODE=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -y|--yes)  ASSUME_YES=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf '未知参数：%s\n\n' "$1" >&2; usage; exit 2 ;;
  esac
done

init_dirs
detect_os

if [ "$JSON_MODE" != "1" ]; then
  printf '\n%s\n' "${C_BLUE}Vmbra 一键环境配置${C_RESET}"
  printf '%s\n' "${C_DIM}系统：$OS_NAME/$ARCH · 日志：$LOG_FILE${C_RESET}"
fi

# ---------- 授权 ----------
# 客户端主程序已校验过时传 --skip-license，避免重复请求授权中心。
if [ "$SKIP_LICENSE" = "1" ]; then
  step_start license "校验授权"
  step_skip "已由客户端完成授权校验"
else
  if [ -z "$LICENSE_KEY" ]; then
    if [ -t 0 ]; then
      printf '\n请输入授权码：'
      read -r LICENSE_KEY
    else
      die "缺少授权码，请使用 --license 参数"
    fi
  fi

  LICENSE_KEY="$(printf '%s' "$LICENSE_KEY" | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]')"
  if [ -z "$LICENSE_KEY" ]; then die "授权码为空"; fi

  step_start license "校验授权"
  FAIL_REASON=""
  ensure_license "$LICENSE_KEY" "$LICENSE_SERVER"
  rc=$?
  if [ "$rc" -eq 2 ]; then
    die "${FAIL_REASON:-授权校验失败}"
  elif [ "$rc" -ne 0 ]; then
    die "无法连接授权中心（${LICENSE_SERVER}）。请检查网络或联系服务商。"
  fi
  step_ok "授权有效"
fi

# ---------- 决定出口节点 ----------
VPS_HOST=""
VPS_PORT="443"
if [ -n "$VPS_OVERRIDE" ]; then
  VPS_HOST="${VPS_OVERRIDE%%:*}"
  case "$VPS_OVERRIDE" in
    *:*) VPS_PORT="${VPS_OVERRIDE##*:}" ;;
  esac
else
  VPS_HOST="$(state_read vps_host)"
  VPS_PORT="$(state_read vps_port)"
  [ -z "$VPS_PORT" ] && VPS_PORT="443"
fi

if [ "$JSON_MODE" != "1" ] && [ -n "$VPS_HOST" ]; then
  printf '%s\n' "${C_DIM}出口节点：$VPS_HOST:$VPS_PORT${C_RESET}"
fi

# ---------- 执行安装 ----------
# 每个工具都是「勾选才装」，未勾选也输出一条 skip，进度分母保持 12。
step_clt
step_brew

if tool_wanted git; then
  step_git
else
  step_start git "安装 Git"
  step_skip "未勾选安装"
fi

if tool_wanted node; then
  step_node
else
  step_start node "安装 Node.js LTS"
  step_skip "未勾选安装"
fi

step_vps "$VPS_HOST" "$VPS_PORT"

if [ "$SKIP_VM" = "1" ] || ! tool_wanted vm; then
  step_start vm "准备 lima 隔离虚拟机"
  step_skip "未勾选 Linux 虚拟机终端"
  step_start guest "虚拟机内安装基础组件"
  step_skip "未勾选 Linux 虚拟机终端"
else
  step_vm
  step_guest
fi

if tool_wanted chrome; then
  step_chrome
else
  step_start chrome "安装 Google Chrome"
  step_skip "未勾选安装"
fi

if tool_wanted claude; then
  step_claude
else
  step_start claude "安装 Claude Code"
  step_skip "未勾选安装"
fi

step_profile

if ! step_verify; then
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"done\",\"ok\":false,\"error\":\"环境自检未通过\"}"
  fi
  printf '\n%s\n' "${C_RED}环境自检未通过，请查看日志：$LOG_FILE${C_RESET}" >&2
  exit 1
fi

# ---------- 完成 ----------
if [ "$JSON_MODE" = "1" ]; then
  emit_json "{\"event\":\"done\",\"ok\":true,\"vpsHost\":$(json_str "$VPS_HOST"),\"vpsPort\":$(json_str "$VPS_PORT"),\"logFile\":$(json_str "$LOG_FILE")}"
else
  printf '\n%s\n' "${C_GREEN}环境已就绪${C_RESET}"
  printf '%s\n' "  已选工具 : $TOOLS"
  printf '%s\n' "  Node     : $(node -v 2>/dev/null || echo '未安装')"
  printf '%s\n' "  Git      : $(git --version 2>/dev/null | head -1 || echo '未安装')"
  printf '%s\n' "  Claude   : $(PATH="$NPM_GLOBAL_PREFIX/bin:$PATH" command -v claude 2>/dev/null || echo '未安装')"
  printf '%s\n' "  Chrome   : $([ -d '/Applications/Google Chrome.app' ] && echo '已安装' || echo '未安装')"
  [ -n "$VPS_HOST" ] && printf '%s\n' "  出口     : $VPS_HOST:$VPS_PORT"
  printf '%s\n' "  日志   : $LOG_FILE"
  printf '\n%s\n' "${C_DIM}提示：新开终端或执行 source \"$SHELL_PROFILE\" 后即可使用 claude 命令${C_RESET}"
fi

exit 0
