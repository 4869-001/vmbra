#!/usr/bin/env bash
# 各安装步骤：每一步都幂等，已就绪则跳过
# 依赖 common.sh

TOOLCHAIN_DIR="$VMBRA_HOME/toolchain"
NODE_VERSION="${VMBRA_NODE_VERSION:-22.11.0}"
NPM_GLOBAL_PREFIX="$TOOLCHAIN_DIR/npm-global"
CLAUDE_PKG="${VMBRA_CLAUDE_PKG:-@anthropic-ai/claude-code}"

# ---------- 1. Xcode Command Line Tools（macOS） ----------
step_clt() {
  step_start clt "检查 Xcode 命令行工具"
  if [ "$OS_NAME" != "Darwin" ]; then
    step_skip "非 macOS，无需 Xcode CLT"
    return 0
  fi
  if xcode-select -p >/dev/null 2>&1; then
    step_ok "$(xcode-select -p)"
    return 0
  fi
  info "未检测到 CLT，触发系统安装弹窗（需要人工点确认）"
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "xcode-select --install"
    step_skip "dry-run 跳过 CLT 安装"
    return 0
  fi
  xcode-select --install >/dev/null 2>&1 || true
  local waited=0
  while ! xcode-select -p >/dev/null 2>&1; do
    sleep 10
    waited=$((waited + 10))
    if [ "$waited" -ge "${CLT_WAIT_SECONDS:-600}" ]; then
      step_warn "等待超时。请手动完成安装后重新运行：xcode-select --install"
      return 0
    fi
    [ $((waited % 60)) -eq 0 ] && info "等待 CLT 安装中… ${waited}s"
  done
  step_ok
}

# ---------- 2. Homebrew（可选） ----------
step_brew() {
  step_start brew "准备包管理器"
  if have brew; then
    step_skip "$(brew --version | head -1)"
    return 0
  fi
  case "$OS_NAME" in
    Darwin)
      info "未检测到 Homebrew。将安装到 /opt/homebrew（需要 sudo 密码）"
      if [ "$DRY_RUN" = "1" ]; then
        step_skip "dry-run 跳过 Homebrew 安装"
        return 0
      fi
      NONINTERACTIVE=1 /bin/bash -c \
        "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)" >> "$LOG_FILE" 2>&1 || {
        step_warn "Homebrew 安装失败，后续将使用免 Homebrew 的安装方式"
        return 0
      }
      if [ -x /opt/homebrew/bin/brew ]; then eval "$(/opt/homebrew/bin/brew shellenv)"; fi
      if [ -x /usr/local/bin/brew ]; then eval "$(/usr/local/bin/brew shellenv)"; fi
      have brew && step_ok "$(brew --version | head -1)" || step_warn "Homebrew 不可用，继续使用替代方案"
      ;;
    *)
      step_warn "非 macOS 环境，跳过 Homebrew，使用系统包管理器"
      ;;
  esac
}

# ---------- 3. Git ----------
step_git() {
  step_start git "安装 Git"
  if have git; then
    step_skip "$(git --version 2>/dev/null)"
    return 0
  fi
  if have brew; then
    die_on_fail "Git 安装失败" brew install git
  elif [ "$OS_NAME" = "Darwin" ]; then
    # CLT 自带 git，通常只是 PATH 未刷新
    step_warn "尝试通过 CLT 获取 git 失败，请重开终端后重试"
    return 0
  else
    die_on_fail "Git 安装失败" sudo apt-get install -y git
  fi
  step_ok "$(git --version 2>/dev/null)"
}

# ---------- 4. Node.js ----------
node_ok() {
  have node || return 1
  local major
  major="$(node -v 2>/dev/null | sed 's/^v//' | cut -d. -f1)"
  [ -n "$major" ] && [ "$major" -ge 18 ]
}

step_node() {
  step_start node "安装 Node.js LTS"
  if node_ok; then
    step_skip "$(node -v) / npm $(npm -v 2>/dev/null)"
    step_node_paths
    return 0
  fi

  install_node_fallback() {
    local os arch tarball url
    case "$OS_NAME" in
      Darwin) os="darwin" ;;
      Linux)  os="linux" ;;
      *) die "不支持的平台：$OS_NAME" ;;
    esac
    arch="$ARCH"
    tarball="node-v${NODE_VERSION}-${os}-${arch}"
    url="https://nodejs.org/dist/v${NODE_VERSION}/${tarball}.tar.gz"
    info "下载 $url"
    mkdir -p "$TOOLCHAIN_DIR"
    if [ "$DRY_RUN" = "1" ]; then
      dry_cmd "curl -fsSL $url | tar -xz -C $TOOLCHAIN_DIR"
      step_skip "dry-run 跳过 Node.js 下载"
      return 0
    fi
    curl -fsSL "$url" -o "$TOOLCHAIN_DIR/node.tar.gz" >> "$LOG_FILE" 2>&1 || die "Node.js 下载失败，请检查网络"
    tar -xzf "$TOOLCHAIN_DIR/node.tar.gz" -C "$TOOLCHAIN_DIR" >> "$LOG_FILE" 2>&1 || die "Node.js 解压失败"
    rm -f "$TOOLCHAIN_DIR/node.tar.gz"
    [ -d "$TOOLCHAIN_DIR/node" ] && rm -rf "$TOOLCHAIN_DIR/node"
    mv "$TOOLCHAIN_DIR/$tarball" "$TOOLCHAIN_DIR/node"
    export PATH="$TOOLCHAIN_DIR/node/bin:$PATH"
  }

  if have brew && [ "$OS_NAME" = "Darwin" ]; then
    if ! brew list node >/dev/null 2>&1 && ! brew list node@22 >/dev/null 2>&1; then
      info "通过 Homebrew 安装 Node.js（可能需要几分钟）"
      brew install node >> "$LOG_FILE" 2>&1 || {
        warn "Homebrew 安装 Node 失败，改用官方压缩包"
        install_node_fallback
      }
    fi
  else
    install_node_fallback
  fi

  node_ok || die "Node.js 安装后仍不可用，请查看日志 $LOG_FILE"
  step_ok "$(node -v) / npm $(npm -v 2>/dev/null)"
  step_node_paths
}

# 配置 npm 全局目录到用户空间，避免 sudo
step_node_paths() {
  mkdir -p "$NPM_GLOBAL_PREFIX"
  export NPM_CONFIG_PREFIX="$NPM_GLOBAL_PREFIX"
  export PATH="$NPM_GLOBAL_PREFIX/bin:$PATH"
  if [ -d "$TOOLCHAIN_DIR/node/bin" ]; then
    export PATH="$TOOLCHAIN_DIR/node/bin:$PATH"
  fi
}

# ---------- 5. VPS 出口校验 ----------
step_vps() {
  step_start vps "校验 VPS 出口连通性"
  local host="$1" port="$2"
  if [ -z "$host" ]; then
    step_warn "未获取到可用 VPS 地址，稍后可在「网络」页手动配置"
    return 0
  fi
  if [ "$DRY_RUN" = "1" ]; then
    step_skip "dry-run 跳过端口探测 $host:$port"
    return 0
  fi
  local ok=0
  if have nc; then
    nc -z -G 5 -w 5 "$host" "$port" >/dev/null 2>&1 && ok=1
  else
    (exec 3<>"/dev/tcp/$host/$port") >/dev/null 2>&1 && ok=1
  fi
  if [ "$ok" = "1" ]; then
    step_ok "$host:$port 可达"
  else
    step_warn "$host:$port 连接失败，可能被防火墙拦截或节点下线；环境仍会继续安装"
  fi
}

# ---------- 6. lima 虚拟机 ----------
step_vm() {
  step_start vm "准备 lima 隔离虚拟机"
  if [ "$OS_NAME" != "Darwin" ]; then
    step_skip "非 macOS，使用本机环境"
    return 0
  fi
  if ! have limactl; then
    if have brew && [ "$CREATE_VM" = "1" ]; then
      info "安装 lima"
      brew install lima >> "$LOG_FILE" 2>&1 || true
    fi
  fi
  if ! have limactl; then
    step_warn "未安装 lima（可用 brew install lima 安装），跳过隔离环境步骤"
    return 0
  fi

  local instances
  instances="$(limactl list -q 2>/dev/null || true)"
  VM_NAME="${VMBRA_VM_NAME:-vps}"
  if printf '%s\n' "$instances" | grep -qx "$VM_NAME"; then
    local status
    status="$(limactl list "$VM_NAME" --format '{{.Status}}' 2>/dev/null || echo unknown)"
    if [ "$status" = "Running" ]; then
      step_skip "实例 $VM_NAME 已在运行"
    else
      info "启动实例 $VM_NAME"
      limactl start "$VM_NAME" >> "$LOG_FILE" 2>&1 || {
        step_warn "启动 $VM_NAME 失败，请查看日志"
        return 0
      }
      step_ok "实例 $VM_NAME 已启动"
    fi
    return 0
  fi

  if [ "$CREATE_VM" != "1" ]; then
    step_warn "未找到实例 ${VM_NAME}；如需自动创建请加 --create-vm（会下载约 1GB 镜像）"
    return 0
  fi

  info "创建 lima 实例 ${VM_NAME}（首次下载镜像，耗时较长）"
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "limactl start --name=$VM_NAME --tty=false template://default"
    step_skip "dry-run 跳过虚拟机创建"
    return 0
  fi
  limactl start --name="$VM_NAME" --tty=false template://default >> "$LOG_FILE" 2>&1 || {
    step_warn "创建虚拟机失败，请查看日志"
    return 0
  }
  step_ok "实例 $VM_NAME 已创建"
}

# ---------- 7. 虚拟机内环境 ----------
step_guest() {
  step_start guest "虚拟机内安装 git / node / npm"
  if [ "$OS_NAME" != "Darwin" ] || ! have limactl || [ -z "$VM_NAME" ]; then
    step_skip "无隔离虚拟机，跳过"
    return 0
  fi
  limactl list -q 2>/dev/null | grep -qx "$VM_NAME" || { step_skip "实例不存在"; return 0; }

  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "limactl shell $VM_NAME -- sudo apt-get install -y git curl ca-certificates nodejs npm"
    step_skip "dry-run 跳过客户机安装"
    return 0
  fi

  if limactl shell "$VM_NAME" -- bash -lc 'command -v git && command -v node' >/dev/null 2>&1; then
    step_skip "客户机环境已就绪"
    return 0
  fi

  info "在 $VM_NAME 内安装基础软件包（Ubuntu/Debian 系）"
  limactl shell "$VM_NAME" -- sudo apt-get update -qq >> "$LOG_FILE" 2>&1 || true
  if limactl shell "$VM_NAME" -- sudo apt-get install -y -qq git curl ca-certificates nodejs npm >> "$LOG_FILE" 2>&1; then
    step_ok
  else
    step_warn "客户机安装未完成（非 apt 发行版或网络受限），可在控制台手动重试"
  fi
}

# ---------- 7.5 Google Chrome ----------
step_chrome() {
  step_start chrome "安装 Google Chrome"
  if [ "$OS_NAME" != "Darwin" ]; then
    step_skip "非 macOS，跳过"
    return 0
  fi
  local app="/Applications/Google Chrome.app"
  local user_app="$HOME/Applications/Google Chrome.app"
  if [ -d "$app" ] || [ -d "$user_app" ]; then
    step_skip "已安装"
    return 0
  fi
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "brew install --cask google-chrome"
    step_skip "dry-run 跳过 Chrome 下载"
    return 0
  fi
  if ! have brew; then
    step_warn "未安装 Homebrew，无法自动安装 Chrome；可继续用系统浏览器"
    return 0
  fi
  info "通过 Homebrew 安装 Google Chrome（约 180MB）"
  if brew install --cask google-chrome >> "$LOG_FILE" 2>&1; then
    step_ok "已安装"
  else
    step_warn "Chrome 安装失败，可手动安装后重新运行：https://www.google.com/chrome/"
  fi
}

# ---------- 8. Claude Code ----------
step_claude() {
  step_start claude "安装 Claude Code"
  step_node_paths
  if have claude; then
    step_skip "$(claude --version 2>/dev/null | head -1)"
    return 0
  fi
  if ! have npm; then
    step_warn "npm 不可用，无法安装 Claude Code"
    return 0
  fi
  info "npm install -g $CLAUDE_PKG"
  if [ "$DRY_RUN" = "1" ]; then
    dry_cmd "npm install -g $CLAUDE_PKG --prefix $NPM_GLOBAL_PREFIX"
    step_skip "dry-run 跳过 Claude Code 安装"
    return 0
  fi
  if npm install -g "$CLAUDE_PKG" --prefix "$NPM_GLOBAL_PREFIX" --no-fund --no-audit >> "$LOG_FILE" 2>&1; then
    hash -r 2>/dev/null || true
    step_ok "$(PATH="$NPM_GLOBAL_PREFIX/bin:$PATH" claude --version 2>/dev/null | head -1)"
  else
    step_warn "Claude Code 安装失败，请查看日志：$LOG_FILE"
  fi
}

# ---------- 9. 写入 shell 配置 ----------
step_profile() {
  step_start profile "写入终端环境变量"
  detect_shell_profile
  local body
  body="export VMBRA_HOME=\"$VMBRA_HOME\"
export NPM_CONFIG_PREFIX=\"$NPM_GLOBAL_PREFIX\"
export PATH=\"$NPM_GLOBAL_PREFIX/bin:$VMBRA_HOME/toolchain/node/bin:\$PATH\""
  ensure_shell_block "vmbra-env" "$body"
  step_ok "${SHELL_PROFILE}"
}

# ---------- 10. 自检 ----------
# 只检查勾选过的工具：客户取消勾选的项不参与判定，也不影响退出码。
step_verify() {
  step_start verify "环境自检"
  local results=""
  local failed=0 bin id
  for bin in git node npm claude; do
    id="$bin"
    [ "$bin" = "npm" ] && id="node"
    tool_wanted "$id" || continue
    if have "$bin"; then
      results="$results ${bin}✓"
    else
      results="$results ${bin}✗"
      # Claude Code 属于可选工具，缺失只提示不阻断
      [ "$bin" != "claude" ] && failed=1
    fi
  done
  if tool_wanted chrome; then
    if [ -d "/Applications/Google Chrome.app" ] || [ -d "$HOME/Applications/Google Chrome.app" ]; then
      results="$results chrome✓"
    else
      results="$results chrome✗"
    fi
  fi
  if [ -z "$results" ]; then
    step_ok "未勾选任何工具，仅保留基础环境"
    return 0
  fi
  if [ "$failed" = "1" ]; then
    step_fail "关键组件缺失：$results"
    return 1
  fi
  if tool_wanted claude && ! have claude; then
    step_warn "基础环境就绪，但 Claude Code 未安装：${results}（可重新运行本脚本重试）"
    return 0
  fi
  step_ok "$results"
  return 0
}
