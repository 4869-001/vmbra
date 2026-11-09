#!/usr/bin/env bash
# 公共工具函数：日志、进度上报、命令探测
# 由 setup.sh source，不单独执行

VMBRA_HOME="${VMBRA_HOME:-$HOME/.vmbra}"
VMBRA_LOG_DIR="$VMBRA_HOME/logs"
VMBRA_STATE_DIR="$VMBRA_HOME/state"
JSON_MODE="${JSON_MODE:-0}"
DRY_RUN="${DRY_RUN:-0}"
LOG_FILE=""

C_RESET=""; C_DIM=""; C_BLUE=""; C_GREEN=""; C_RED=""; C_YELLOW=""
if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
  C_RESET=$'\033[0m'; C_DIM=$'\033[2m'; C_BLUE=$'\033[34m'
  C_GREEN=$'\033[32m'; C_RED=$'\033[31m'; C_YELLOW=$'\033[33m'
fi

init_dirs() {
  mkdir -p "$VMBRA_LOG_DIR" "$VMBRA_STATE_DIR"
  LOG_FILE="$VMBRA_LOG_DIR/setup-$(date +%Y%m%d-%H%M%S).log"
  : > "$LOG_FILE"
}

_ts() { date '+%H:%M:%S'; }

# 机器可读的事件（供 vmbra 控制台解析）
emit_json() {
  [ "$JSON_MODE" = "1" ] || return 0
  printf '%s\n' "$1"
}

log() {
  local msg="$*"
  printf '%s\n' "[$(_ts)] $msg" >> "$LOG_FILE" 2>/dev/null || true
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "$(json_escape_event "log" "$msg")"
  else
    printf '%s\n' "${C_DIM}[$(_ts)]${C_RESET} $msg"
  fi
}

info()  { log "$*"; }
warn()  {
  printf '%s\n' "[$(_ts)] WARN $*" >> "$LOG_FILE" 2>/dev/null || true
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"log\",\"level\":\"warn\",\"message\":$(json_str "$*")}"
  else
    printf '%s\n' "${C_YELLOW}! $*${C_RESET}"
  fi
}

json_str() {
  # 轻量 JSON 字符串转义
  local s="$1"
  s=${s//\\/\\\\}
  s=${s//\"/\\\"}
  s=${s//$'\n'/\\n}
  s=${s//$'\t'/\\t}
  printf '"%s"' "$s"
}

json_escape_event() {
  local kind="$1" msg="$2"
  printf '{"event":%s,"level":"info","message":%s}' "$(json_str "$kind")" "$(json_str "$msg")"
}

step_start() {
  local id="$1" title="$2"
  CURRENT_STEP="$id"
  STEP_START_TS=$(date +%s)
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"step\",\"id\":$(json_str "$id"),\"title\":$(json_str "$title"),\"status\":\"running\"}"
  else
    printf '%s\n' ""
    printf '%s\n' "${C_BLUE}▸ $title${C_RESET}"
  fi
  log "STEP START $id $title"
}

step_ok() {
  local detail="${1:-}"
  local dur=$(( $(date +%s) - STEP_START_TS ))
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"step\",\"id\":$(json_str "$CURRENT_STEP"),\"status\":\"ok\",\"durationMs\":$((dur*1000)),\"detail\":$(json_str "$detail")}"
  else
    printf '%s\n' "  ${C_GREEN}✓${C_RESET} 完成 (${dur}s)${detail:+ — $detail}"
  fi
  log "STEP OK $CURRENT_STEP $detail"
}

step_skip() {
  local detail="${1:-已就绪}"
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"step\",\"id\":$(json_str "$CURRENT_STEP"),\"status\":\"skip\",\"detail\":$(json_str "$detail")}"
  else
    printf '%s\n' "  ${C_DIM}– 跳过：$detail${C_RESET}"
  fi
  log "STEP SKIP $CURRENT_STEP $detail"
}

step_warn() {
  local detail="${1:-}"
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"step\",\"id\":$(json_str "$CURRENT_STEP"),\"status\":\"warn\",\"detail\":$(json_str "$detail")}"
  else
    printf '%s\n' "  ${C_YELLOW}! $detail${C_RESET}"
  fi
  log "STEP WARN $CURRENT_STEP $detail"
}

step_fail() {
  local detail="${1:-}"
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"step\",\"id\":$(json_str "$CURRENT_STEP"),\"status\":\"fail\",\"detail\":$(json_str "$detail")}"
  else
    printf '%s\n' "  ${C_RED}✗ 失败：$detail${C_RESET}" >&2
  fi
  log "STEP FAIL $CURRENT_STEP $detail"
}

die() {
  step_fail "$1"
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"done\",\"ok\":false,\"error\":$(json_str "$1")}"
  else
    printf '\n%s\n' "${C_RED}安装中止：$1${C_RESET}" >&2
    printf '%s\n' "${C_DIM}完整日志：$LOG_FILE${C_RESET}" >&2
  fi
  exit 1
}

have() { command -v "$1" >/dev/null 2>&1; }

# dry-run 模式下打印（JSON 模式输出结构化事件，避免污染 NDJSON 流）
dry_cmd() {
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"cmd\",\"dryRun\":true,\"command\":$(json_str "$*")}"
  else
    printf '%s\n' "  ${C_DIM}[dry-run] $*${C_RESET}"
  fi
}

# 执行命令，dry-run 时只打印
run() {
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "$@"
    return 0
  fi
  if [ "$JSON_MODE" = "1" ]; then
    emit_json "{\"event\":\"cmd\",\"command\":$(json_str "$*")}"
  else
    printf '%s\n' "  ${C_DIM}\$ $*${C_RESET}"
  fi
  printf '%s\n' "\$ $*" >> "$LOG_FILE"
  "$@" >> "$LOG_FILE" 2>&1
}

die_on_fail() {
  local msg="$1"; shift
  if ! run "$@"; then
    die "$msg"
  fi
}

detect_os() {
  OS_NAME="$(uname -s)"
  ARCH_RAW="$(uname -m)"
  case "$ARCH_RAW" in
    arm64|aarch64) ARCH="arm64" ;;
    x86_64|amd64)  ARCH="x64" ;;
    *) ARCH="$ARCH_RAW" ;;
  esac
}

detect_shell_profile() {
  local shell_name
  shell_name="$(basename "${SHELL:-/bin/zsh}")"
  case "$shell_name" in
    zsh)  SHELL_PROFILE="$HOME/.zshrc" ;;
    bash) SHELL_PROFILE="$HOME/.bash_profile" ;;
    *)    SHELL_PROFILE="$HOME/.profile" ;;
  esac
}

# 确保 profile 里有一段可重复执行不重复追加的配置
ensure_shell_block() {
  local marker="$1" body="$2"
  local profile="${SHELL_PROFILE:-$HOME/.zshrc}"
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "写入 $profile ($marker)"
    return 0
  fi
  touch "$profile"
  if grep -qF "# >>> $marker >>>" "$profile" 2>/dev/null; then
    # 用 awk 替换已有块
    local tmp="$profile.vmbra.tmp"
    awk -v m="$marker" -v body="$body" '
      $0 ~ ("# >>> " m " >>>") { print; print body; skip=1; next }
      $0 ~ ("# <<< " m " <<<") { skip=0; print; next }
      skip==1 { next }
      { print }
    ' "$profile" > "$tmp" && mv "$tmp" "$profile"
  else
    {
      printf '\n# >>> %s >>>\n' "$marker"
      printf '%s\n' "$body"
      printf '# <<< %s <<<\n' "$marker"
    } >> "$profile"
  fi
}
