#!/usr/bin/env bash
# Builds the Linux windows app (automator-windows: Fyne, so cgo, OpenGL, X11
# and Wayland) for one architecture, in a Docker container: cross-compiling
# cgo from macOS needs Linux's C libraries, which the container has
# (scripts/linux-windows.Dockerfile). Any Docker works (OrbStack, Docker
# Desktop, Colima, Docker on Linux); the Go module and build caches are
# kept in Docker volumes, so builds after the first are quick.
# Usage: build-linux-windows.sh <tray-dir> <amd64|arm64> <output-binary>
set -euo pipefail
scripts="$(cd "$(dirname "$0")" && pwd)"
tray=${1:?usage: build-linux-windows.sh <tray-dir> <amd64|arm64> <output-binary>}
arch=${2:?usage: build-linux-windows.sh <tray-dir> <amd64|arm64> <output-binary>}
out=${3:?usage: build-linux-windows.sh <tray-dir> <amd64|arm64> <output-binary>}
die() { echo "build-linux-windows.sh: $*" >&2; exit 1; }

case $arch in
  amd64) triplet=x86_64-linux-gnu ;;
  arm64) triplet=aarch64-linux-gnu ;;
  *) die "unknown architecture $arch (amd64, arm64)" ;;
esac
command -v docker >/dev/null ||
  die "Docker is needed to build the Linux windows app (install OrbStack, Docker Desktop or Colima; see docs/Guides/Developing.md), or package without it: LINUX_WINDOWS=none"
docker info >/dev/null 2>&1 ||
  die "Docker isn't running: start it (OrbStack, Docker Desktop, Colima), or package without the windows app: LINUX_WINDOWS=none"

tray=$(cd "$tray" && pwd)
go_version=$(sed -n 's/^go //p' "$tray/windows/go.mod")
[ -n "$go_version" ] || die "no go version in $tray/windows/go.mod"
image="automator-windows-build:go$go_version"
docker build -q -t "$image" --build-arg "GO_IMAGE=golang:$go_version-bookworm" - < "$scripts/linux-windows.Dockerfile" >/dev/null ||
  die "couldn't make the build image $image"

mkdir -p "$(dirname "$out")"
out_dir=$(cd "$(dirname "$out")" && pwd)
name=$(basename "$out")
# Built as root in the container, then handed to me (Docker on Linux would
# leave it root's).
docker run --rm \
  -v "$tray:/src:ro" -v "$out_dir:/out" \
  -v automator-windows-gomod:/go/pkg/mod -v automator-windows-gocache:/root/.cache/go-build \
  -w /src/windows \
  -e CGO_ENABLED=1 -e GOOS=linux -e GOARCH="$arch" -e CC="$triplet-gcc" \
  -e PKG_CONFIG_PATH= -e PKG_CONFIG_LIBDIR="/usr/lib/$triplet/pkgconfig:/usr/share/pkgconfig" \
  "$image" sh -c "go build -trimpath -ldflags '-s -w' -o '/out/$name' . && chown $(id -u):$(id -g) '/out/$name'"
echo "$out"
