#!/usr/bin/env bash
# 打包桌面外壳为 dist/Vmbra.app。
# 只依赖 Command Line Tools 里的 swiftc，不需要 Xcode 工程。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
OUT="${OUT:-$ROOT/dist}"
APP="$OUT/Vmbra.app"
IDENTITY="${CODESIGN_IDENTITY:--}"   # 默认 ad-hoc；分发时传 Developer ID

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$HERE/Info.plist" "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

# 应用图标：先跑 scripts/make-icon.sh 生成，缺失时构建脚本自动补一次。
if [[ ! -f "$HERE/vmbra.icns" ]]; then
  echo "缺少 vmbra.icns，先生成图标…"
  "$ROOT/scripts/make-icon.sh" >/dev/null
fi
cp "$HERE/vmbra.icns" "$APP/Contents/Resources/vmbra.icns"

echo "编译 Swift 外壳…"
swiftc -O -swift-version 5 "$HERE/main.swift" \
  -o "$APP/Contents/MacOS/Vmbra" \
  -framework AppKit -framework WebKit

# 把 vmbrad 一起塞进包里，桌面程序就能自己拉起管理服务。
if [[ -x "$ROOT/bin/vmbrad" ]]; then
  cp "$ROOT/bin/vmbrad" "$APP/Contents/Resources/vmbrad"
else
  echo "提示：bin/vmbrad 不存在，先执行 make build 才能自带服务端" >&2
fi

# 一键配置脚本随包分发，供「一键配置环境」按钮调用。
if [[ -d "$ROOT/scripts/vmbra-setup" ]]; then
  mkdir -p "$APP/Contents/Resources/scripts"
  cp -R "$ROOT/scripts/vmbra-setup" "$APP/Contents/Resources/scripts/"
  chmod +x "$APP/Contents/Resources/scripts/vmbra-setup/setup.sh"
fi

if [[ "$IDENTITY" == "-" ]]; then
  # 本地自用，ad-hoc 签名即可。
  codesign --force --sign - "$APP" >/dev/null 2>&1 || true
else
  codesign --force --options runtime --timestamp \
    --sign "$IDENTITY" "$APP"
fi
echo "已生成 $APP"
