#!/usr/bin/env bash
# Builds the Linux windows app (automator-windows: Fyne, so cgo, OpenGL, X11
# and Wayland) for one architecture, in a Docker container: cross-compiling
# cgo from macOS needs Linux's C libraries, which the container has
# (scripts/linux-windows.Dockerfile). Any Docker works (OrbStack, Docker
# Desktop, Colima, Docker on Linux).
# Two caches keep it from compiling cold:
#   - the binary itself, by what it's built from (the tray folder's files,
#     the architecture, Go's version and the image's Dockerfile), in
#     $PA_BUILD_CACHE (default: ~/Library/Caches/process-automator-build on
#     macOS, $XDG_CACHE_HOME/process-automator-build elsewhere): the same
#     sources give the same binary, so a package built again (make publish
#     after make ci, another app on the same process-automator) copies it.
#     The newest 8 are kept; NO_BUILD_CACHE=1 builds anyway
#   - Go's module and build caches, in Docker volumes named for Go's
#     version; those for another version (and its image) are removed, so a
#     toolchain change starts clean
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
tray=$(cd "$tray" && pwd)
go_version=$(sed -n 's/^go //p' "$tray/windows/go.mod")
[ -n "$go_version" ] || die "no go version in $tray/windows/go.mod"
mkdir -p "$(dirname "$out")"

# The binary, when one was built from the same inputs.
sha() { shasum -a 256 | cut -d' ' -f1; }
sources=$(cd "$tray" && find . -type f ! -path './.git/*' -print0 | LC_ALL=C sort -z | xargs -0 shasum -a 256 | sha)
key=$(printf '%s\n' "$sources" "$arch" "$go_version" "$(sha < "$scripts/linux-windows.Dockerfile")" | sha)
if [[ $(uname) == Darwin ]]; then default_cache=$HOME/Library/Caches/process-automator-build
else default_cache=${XDG_CACHE_HOME:-$HOME/.cache}/process-automator-build; fi
cache="${PA_BUILD_CACHE:-$default_cache}/linux-windows"
if [[ -z ${NO_BUILD_CACHE:-} && -f $cache/$key/automator-windows ]]; then
  cp "$cache/$key/automator-windows" "$out"
  touch "$cache/$key"
  echo "build-linux-windows.sh: $arch reused (built before from the same sources: $cache/$key)"
  echo "$out"
  exit 0
fi

command -v docker >/dev/null ||
  die "Docker is needed to build the Linux windows app (install OrbStack, Docker Desktop or Colima; see docs/Guides/Developing.md), or package without it: LINUX_WINDOWS=none"
docker info >/dev/null 2>&1 ||
  die "Docker isn't running: start it (OrbStack, Docker Desktop, Colima), or package without the windows app: LINUX_WINDOWS=none"

image="automator-windows-build:go$go_version"
gomod="automator-windows-gomod-go$go_version" gocache="automator-windows-gocache-go$go_version"
# Another Go's caches and image: a toolchain change starts clean.
for v in $(docker volume ls -q --filter name=automator-windows-go); do
  [[ $v == "$gomod" || $v == "$gocache" ]] || docker volume rm "$v" >/dev/null 2>&1 || true
done
for i in $(docker image ls --format '{{.Repository}}:{{.Tag}}' automator-windows-build); do
  [[ $i == "$image" ]] || docker image rm "$i" >/dev/null 2>&1 || true
done
docker build -q -t "$image" --build-arg "GO_IMAGE=golang:$go_version-bookworm" - < "$scripts/linux-windows.Dockerfile" >/dev/null ||
  die "couldn't make the build image $image"

out_dir=$(cd "$(dirname "$out")" && pwd)
name=$(basename "$out")
# Built as root in the container, then handed to me (Docker on Linux would
# leave it root's).
docker run --rm \
  -v "$tray:/src:ro" -v "$out_dir:/out" \
  -v "$gomod:/go/pkg/mod" -v "$gocache:/root/.cache/go-build" \
  -w /src/windows \
  -e CGO_ENABLED=1 -e GOOS=linux -e GOARCH="$arch" -e CC="$triplet-gcc" \
  -e PKG_CONFIG_PATH= -e PKG_CONFIG_LIBDIR="/usr/lib/$triplet/pkgconfig:/usr/share/pkgconfig" \
  "$image" sh -c "go build -trimpath -ldflags '-s -w' -o '/out/$name' . && chown $(id -u):$(id -g) '/out/$name'"

# Kept for the next build from the same inputs; the newest 8 stay.
mkdir -p "$cache/$key.tmp"
cp "$out" "$cache/$key.tmp/automator-windows"
rm -rf "${cache:?}/${key:?}" && mv "$cache/$key.tmp" "$cache/$key"
# shellcheck disable=SC2012 # names are hex keys
ls -t "$cache" | tail -n +9 | while read -r old; do rm -rf "${cache:?}/$old"; done
echo "$out"
