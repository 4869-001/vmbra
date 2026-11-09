#!/usr/bin/env bash
# 打包桌面外壳为 dist/AnyWhere.app。
# 只依赖 Command Line Tools 里的 swiftc，不需要 Xcode 工程。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
OUT="${OUT:-$ROOT/dist}"
APP="$OUT/AnyWhere.app"

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
cp "$HERE/Info.plist" "$APP/Contents/Info.plist"
printf 'APPL????' > "$APP/Contents/PkgInfo"

echo "编译 Swift 外壳…"
swiftc -O -swift-version 5 "$HERE/main.swift" \
  -o "$APP/Contents/MacOS/AnyWhere" \
  -framework AppKit -framework WebKit

# 把 anywhered 一起塞进包里，桌面程序就能自己拉起管理服务。
if [[ -x "$ROOT/bin/anywhered" ]]; then
  cp "$ROOT/bin/anywhered" "$APP/Contents/Resources/anywhered"
else
  echo "提示：bin/anywhered 不存在，先执行 make build 才能自带服务端" >&2
fi

# 一键配置脚本随包分发，供「一键配置环境」按钮调用。
if [[ -d "$ROOT/scripts/vmbra-setup" ]]; then
  mkdir -p "$APP/Contents/Resources/scripts"
  cp -R "$ROOT/scripts/vmbra-setup" "$APP/Contents/Resources/scripts/"
  chmod +x "$APP/Contents/Resources/scripts/vmbra-setup/setup.sh"
fi

# 本地自用，ad-hoc 签名即可。
codesign --force --sign - "$APP" >/dev/null 2>&1 || true
echo "已生成 $APP"
