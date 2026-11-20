#!/bin/sh
# render.sh <outdir> [--connected]: the logo SVGs, iconset, .icns, 1024 PNG and preview sheets (see README.md).
set -e
cd "$(dirname "$0")"
O="$1"; shift
./venv/bin/python build.py "$O/" "$@"
rm -rf "$O/AppIcon.iconset" && mkdir "$O/AppIcon.iconset"
R() { rsvg-convert -w "$2" "$O/$1.svg" -o "$O/AppIcon.iconset/$3.png"; }
R AppIcon-small 16 icon_16x16; R AppIcon-small 32 icon_16x16@2x; R AppIcon-small 32 icon_32x32; R AppIcon 64 icon_32x32@2x
for s in 128 256 512; do R AppIcon $s icon_${s}x${s}; R AppIcon $((s*2)) icon_${s}x${s}@2x; done
iconutil -c icns "$O/AppIcon.iconset" -o "$O/AppIcon.icns"
cp "$O/AppIcon.iconset/icon_512x512@2x.png" "$O/AppIcon-1024.png"
mkdir -p "$O/previews"
./iconsheet "$O/AppIcon.iconset" "$O/previews/app-icon-sizes-light.png" light
./iconsheet "$O/AppIcon.iconset" "$O/previews/app-icon-sizes-dark.png" dark
./menubar "$O/glyph.svg" "$O/dot.svg" "$O/previews/menubar-light.png" light
./menubar "$O/glyph.svg" "$O/dot.svg" "$O/previews/menubar-dark.png" dark
