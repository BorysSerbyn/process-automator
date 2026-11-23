#!/usr/bin/env bash
# Tests build-linux-windows.sh's caches against a fake docker, so it runs
# anywhere and never starts a container: the binary is built once for the
# same inputs and copied after that; changed sources build again; another
# Go's volumes and image are removed, this one's kept.
set -euo pipefail
repo="$(cd "$(dirname "$0")/.." && pwd)"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fail() { echo "test-build-linux-windows: $*" >&2; exit 1; }

mkdir -p "$tmp/bin" "$tmp/tray/windows"
printf 'module x\n\ngo 1.99.1\n' > "$tmp/tray/windows/go.mod"
echo 'package main' > "$tmp/tray/windows/main.go"
cat > "$tmp/bin/docker" <<'FAKE'
#!/usr/bin/env bash
echo "$*" >> "$FAKE_DOCKER_LOG"
case $1 in
  info|build) ;;
  volume)
    case $2 in
      ls) printf '%s\n' automator-windows-gomod automator-windows-gocache-go1.0.0 automator-windows-gocache-go1.99.1 ;;
    esac ;;
  image)
    case $2 in
      ls) printf '%s\n' automator-windows-build:go1.0.0 automator-windows-build:go1.99.1 ;;
    esac ;;
  run)
    # The output folder is the one mounted at /out; the binary is the last
    # word of the command, after /out/.
    for a in "$@"; do case $a in *:/out) dir=${a%:/out} ;; esac; done
    name=$(sed -E "s#.* -o '/out/([^']+)'.*#\1#" <<<"${*: -1}")
    echo "built $(date +%s%N)" > "$dir/$name" ;;
esac
FAKE
chmod +x "$tmp/bin/docker"
export PATH="$tmp/bin:$PATH" FAKE_DOCKER_LOG="$tmp/docker.log" PA_BUILD_CACHE="$tmp/cache"
build() { "$repo/scripts/build-linux-windows.sh" "$tmp/tray" amd64 "$tmp/out/automator-windows" >/dev/null; }
runs() { grep -c '^run ' "$tmp/docker.log" || true; }

build
[[ $(runs) == 1 ]] || fail "the first build didn't run"
first=$(cat "$tmp/out/automator-windows")
grep -q 'volume rm automator-windows-gomod$' "$tmp/docker.log" || fail "an unversioned volume was kept"
grep -q 'volume rm automator-windows-gocache-go1.0.0' "$tmp/docker.log" || fail "another Go's volume was kept"
! grep -q 'volume rm automator-windows-gocache-go1.99.1' "$tmp/docker.log" || fail "this Go's volume was removed"
grep -q 'image rm automator-windows-build:go1.0.0' "$tmp/docker.log" || fail "another Go's image was kept"
grep -q -- '-v automator-windows-gomod-go1.99.1:/go/pkg/mod' "$tmp/docker.log" || fail "the module cache isn't this Go's volume"

rm "$tmp/out/automator-windows"
build
[[ $(runs) == 1 ]] || fail "the same sources were built again"
[[ $(cat "$tmp/out/automator-windows") == "$first" ]] || fail "the cached binary wasn't the one built"

NO_BUILD_CACHE=1 build
[[ $(runs) == 2 ]] || fail "NO_BUILD_CACHE=1 didn't build"

echo '// changed' >> "$tmp/tray/windows/main.go"
build
[[ $(runs) == 3 ]] || fail "changed sources weren't built"
"$repo/scripts/build-linux-windows.sh" "$tmp/tray" arm64 "$tmp/out/automator-windows" >/dev/null
[[ $(runs) == 4 ]] || fail "another architecture reused amd64's"
echo "test-build-linux-windows: ok"
