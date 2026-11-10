#!/usr/bin/env bash
# 从品牌母版图生成各尺寸图标，只依赖 macOS 自带工具（sips / iconutil）。
#
#   assets/brand/vmbra-1024.png   母版（1024×1024，唯一源）
#     ├── desktop/vmbra.icns                          App 图标
#     └── internal/webui/web/brand/*.png              控制台侧边栏 logo + favicon
#
# 换 logo：替换母版 PNG 后执行 make icon。
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
MASTER="$ROOT/assets/brand/vmbra-1024.png"
WEB="$ROOT/internal/webui/web/brand"
ICONSET="$(mktemp -d)/vmbra.iconset"

if [[ ! -f "$MASTER" ]]; then
  echo "缺少品牌母版：$MASTER" >&2
  exit 1
fi

mkdir -p "$ICONSET" "$WEB"

echo "切各尺寸…"
for spec in "16 icon_16x16" "32 icon_16x16@2x" "32 icon_32x32" "64 icon_32x32@2x" \
            "128 icon_128x128" "256 icon_128x128@2x" "256 icon_256x256" "512 icon_256x256@2x" \
            "512 icon_512x512" "1024 icon_512x512@2x"; do
  set -- $spec
  sips -z "$1" "$1" "$MASTER" --out "$ICONSET/$2.png" >/dev/null
done

echo "打包 icns…"
iconutil -c icns "$ICONSET" -o "$ROOT/desktop/vmbra.icns"

echo "同步控制台用图…"
cp "$ICONSET/icon_128x128.png"   "$WEB/vmbra-mark.png"
cp "$ICONSET/icon_256x256.png"   "$WEB/vmbra-mark@2x.png"
cp "$ICONSET/icon_32x32@2x.png"  "$WEB/favicon.png"

echo "完成：desktop/vmbra.icns"
