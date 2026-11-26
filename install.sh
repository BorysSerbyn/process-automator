#!/usr/bin/env bash
# Installs the app (NAME below) from its latest release (macOS or Linux),
# then offers first-run setup (templates, model backend, sign-ins).
#
#   ./install.sh                         from a checkout
#   curl -fsSL https://github.com/<REPO>/releases/latest/download/install.sh | bash
#                                        public releases (PUBLIC=1)
#   gh api repos/<REPO>/contents/install.sh \
#     -H 'Accept: application/vnd.github.raw' | bash
#                                        private releases
#
# Options:
#   --version <vX.Y.Z>   install that release instead of the latest
#   --from <archive>     install a local artifact (verified against a
#                        SHA256SUMS next to it when there is one)
#   --yes                ask nothing (no terminal needed); setup is left to
#                        `<COMMAND> setup` (or `setup --json` for an LLM)
#   --files-only         only files: no login items started, no keychain (tests)
#   --without-claude-code
#                        install without Claude Code, for routines on another
#                        model backend (otherwise Claude Code at
#                        CLAUDE_CODE_MIN or later is required, unless only
#                        Codex is installed: then routines run on Codex)
# Environment: INSTALL_DIR, BIN_DIR (see scripts/install-app.sh). Public
# releases are downloaded with curl, signed in to nothing. Private ones are
# read with GH_TOKEN or GITHUB_TOKEN, else the first account the GitHub CLI is
# signed in to that can read them (the active one first, then the others).
#
# It downloads the artifact for this platform and its release's SHA256SUMS,
# checks the checksum and, on macOS, the app's notarized Developer ID
# signature by TEAM_ID, then runs the artifact's own installer
# (install-app.sh), the one `make install` and `<COMMAND> update` run.
set -euo pipefail

# The app: where its releases come from, the Apple team that signs the macOS
# app, its name, its CLI's name, its folder name (its artifacts are
# <FOLDER>-<version>-…) and whether its releases are public (1) or private
# (empty). The binary says the same (automator.App) and the build reads the
# names from here (Makefile).
REPO=BorysSerbyn/process-automator
TEAM_ID=
NAME="Process Automator"
COMMAND=procauto
FOLDER=process-automator
PUBLIC=1

# The Claude Code a new install requires: the version the newest of the
# claude-code backend's implementations is for (agent.NewestClaudeCode; a
# test keeps them equal). An install already there keeps running on an older
# Claude Code, through the implementation for its version.
CLAUDE_CODE_MIN=2.1.331

# Public releases are read from GitHub's web pages (WEB) with curl, ignoring
# any .curlrc (-q) so nothing is sent but the request; private ones
# through its API.
WEB=https://github.com
API=https://api.github.com

main() {
  local version="" from="" yes="" files_only="" claude_code=1
  use_codex=""
  while [ $# -gt 0 ]; do
    case $1 in
      --version|--from)
        [ $# -ge 2 ] || die "$1 needs a value (see --help)"
        if [ "$1" = --version ]; then version=$2; else from=$2; fi
        shift 2 ;;
      --yes) yes=1; shift ;;
      --files-only) files_only=1; shift ;;
      --without-claude-code) claude_code=""; shift ;;
      -h|--help)
        # Piped (curl ... | bash), there's no file to read the help from.
        if [ -f "$0" ]; then sed -n '2,/^set -euo/p' "$0" | sed '$d; s/^# \{0,1\}//'
        else echo "Options: --version <vX.Y.Z>, --from <archive>, --yes, --files-only, --without-claude-code (the script's header says more)"; fi
        exit 0 ;;
      *) die "unknown option $1 (see --help)" ;;
    esac
  done

  local os arch
  case $(uname -s) in
    Darwin) os=macos ;;
    Linux) os=linux ;;
    *) die "$NAME runs on macOS and Linux, not $(uname -s)" ;;
  esac
  case $(uname -m) in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "no release for $(uname -m) processors" ;;
  esac
  [ "$os/$arch" != macos/amd64 ] || die "$NAME needs a Mac with Apple silicon: there's no release for Intel Macs"
  prerequisites "$os" "$from"
  [ -z "$claude_code" ] || check_claude_code
  [ -n "$from" ] || [ -n "$PUBLIC" ] || pick_token

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  local archive sums=""
  if [ -n "$from" ]; then
    [ -f "$from" ] || die "no such file: $from"
    archive=$from
    [ -f "$(dirname "$from")/SHA256SUMS" ] && sums="$(dirname "$from")/SHA256SUMS"
  else
    version=${version:-$(latest_tag)}
    [ -n "$version" ] || no_release "$REPO has no releases yet"
    local v=${version#v} name
    if [ "$os" = macos ]; then name="$FOLDER-$v-macos.zip"; else name="$FOLDER-$v-linux-$arch.tar.gz"; fi
    say "Downloading $NAME $v ($name)…"
    download "v$v" "$name"
    download "v$v" SHA256SUMS
    archive="$tmp/$name" sums="$tmp/SHA256SUMS"
  fi

  if [ -n "$sums" ]; then
    verify_sum "$archive" "$sums"
  else
    say "warning: no SHA256SUMS next to $archive: its checksum isn't checked"
  fi

  mkdir "$tmp/unpacked"
  case $archive in
    *.zip) ditto -x -k "$archive" "$tmp/unpacked" ;;
    *.tar.gz) tar -xzf "$archive" -C "$tmp/unpacked" ;;
    *) die "$archive: expected a .zip (macOS) or .tar.gz (Linux)" ;;
  esac
  local installer
  installer=$(find "$tmp/unpacked" -name install-app.sh -type f | head -n 1)
  [ -n "$installer" ] || die "$archive has no installer (install-app.sh)"
  if [ "$os" = macos ]; then verify_app "$(cd "$(dirname "$installer")/../.." && pwd)"; fi

  bash "$installer" install ${files_only:+--files-only}

  local cli="${BIN_DIR:-$HOME/.local/bin}/$COMMAND"
  [ -z "$use_codex" ] || codex_backend "$cli"
  case ":$PATH:" in
    *":$(dirname "$cli"):"*) ;;
    *) say "Note: $(dirname "$cli") isn't on your PATH; add it to use \`$COMMAND\` by name." ;;
  esac
  if [ -z "$yes" ] && have_tty; then
    printf 'Set it up now (templates, model backend, sign-ins)? [Y/n] ' > /dev/tty
    local answer
    read -r answer < /dev/tty || answer=n
    case $answer in
      n|N|no|No) ;;
      *) "$cli" setup < /dev/tty > /dev/tty 2>&1 || true; return 0 ;;
    esac
  fi
  say "Next: \`$COMMAND setup\` picks templates, the model backend and sign-ins (\`$COMMAND setup --json\` lists the steps)."
}

say() { echo "$*" >&2; }
die() { echo "install.sh: $*" >&2; exit 1; }

# no_release: there's no published download for this machine (releases aren't
# published yet, or not for this platform): building from a checkout is the way.
no_release() {
  die "$1. Build it from a checkout of the repository instead: \`make install\` (docs/Guides/Getting started.md, 'Building it from the checkout', says what it needs)"
}
have() { command -v "$1" >/dev/null 2>&1; }

# have_tty: there's a terminal to ask in. /dev/tty can be readable and
# writable (its permissions) yet not open, with no controlling terminal
# (piped from curl over ssh without -t, an agent's shell, cron): only
# opening it says.
have_tty() { { : < /dev/tty > /dev/tty; } 2>/dev/null; }

prerequisites() {
  local os=$1 from=$2 missing=()
  if [ -n "$from" ]; then :
  elif [ -n "$PUBLIC" ]; then have curl || missing+=(curl)
  else have curl || have gh || missing+=("curl (or the GitHub CLI, gh)")
  fi
  if [ "$os" = macos ]; then
    have ditto || missing+=(ditto)
    have codesign || missing+=(codesign)
  else
    have tar || missing+=(tar)
    have sha256sum || missing+=(sha256sum)
    have secret-tool || say "Note: secret-tool isn't installed; sign-ins need it to keep their secrets (package libsecret-tools)."
  fi
  if [ ${#missing[@]} -gt 0 ]; then die "missing: ${missing[*]}"; fi
}

# codex_backend: an install on Codex runs routines on it, unless a backend
# was chosen before (an earlier install's).
codex_backend() {
  local cli=$1
  [ -x "$cli" ] || return 0
  if "$cli" settings get llm.backend --json 2>/dev/null | grep -q '"isDefault": *true'; then
    "$cli" settings set llm.backend codex >/dev/null && say "Model backend: codex (sign in with \`codex login\` if you haven't)."
  fi
}

# check_claude_code: Claude Code is installed, at CLAUDE_CODE_MIN or later;
# without it, Codex is (use_codex: routines run on it).
check_claude_code() {
  local how="install it (https://claude.com/claude-code) or Codex (https://developers.openai.com/codex/cli), or pass --without-claude-code to use another model backend"
  if ! have claude && have codex; then
    say "Claude Code isn't installed; Codex is: routines will run on Codex (the codex backend, experimental)."
    use_codex=1
    return 0
  fi
  have claude || die "$NAME needs Claude Code $CLAUDE_CODE_MIN or later (or Codex), and neither \`claude\` nor \`codex\` is on your PATH: $how"
  local v
  v=$(claude --version 2>/dev/null </dev/null | grep -oE '[0-9]+(\.[0-9]+)+' | head -n 1) || v=""
  [ -n "$v" ] || die "can't tell which Claude Code \`claude\` is (\`claude --version\` printed no version): $NAME needs $CLAUDE_CODE_MIN or later"
  version_at_least "$v" "$CLAUDE_CODE_MIN" ||
    die "$NAME needs Claude Code $CLAUDE_CODE_MIN or later, and yours is $v: update it (\`claude update\`), then run this again"
  say "Claude Code $v OK."
}

# version_at_least <a> <b>: dotted version a is b or later.
version_at_least() {
  local IFS=. i x y
  # shellcheck disable=SC2206 # split on dots
  local a=($1) b=($2)
  for ((i = 0; i < ${#a[@]} || i < ${#b[@]}; i++)); do
    x=${a[i]:-0} y=${b[i]:-0}
    if ((10#$x > 10#$y)); then return 0; fi
    if ((10#$x < 10#$y)); then return 1; fi
  done
}

token() { echo "${GH_TOKEN:-${GITHUB_TOKEN:-}}"; }

# pick_token: without GH_TOKEN or GITHUB_TOKEN, uses (exports as GH_TOKEN)
# the token of the first account the GitHub CLI is signed in to for
# github.com that can read $REPO: the active one first, then the others.
# None that can: stops, naming the accounts tried. Tokens are never printed.
pick_token() {
  [ -z "$(token)" ] && have gh || return 0
  local t login tried=()
  if t=$(gh auth token --hostname github.com 2>/dev/null) && [ -n "$t" ]; then
    if can_read "$t"; then export GH_TOKEN=$t; return 0; fi
    login=$(gh auth status --hostname github.com --active --json hosts --jq '.hosts["github.com"][0].login' 2>/dev/null) || login=""
    tried+=("${login:-the active account}")
  fi
  for login in $(gh auth status --hostname github.com --json hosts --jq '.hosts["github.com"][] | select(.active | not) | .login' 2>/dev/null); do
    t=$(gh auth token --hostname github.com --user "$login" 2>/dev/null) && [ -n "$t" ] || continue
    if can_read "$t"; then export GH_TOKEN=$t; return 0; fi
    tried+=("$login")
  done
  if [ ${#tried[@]} -gt 0 ]; then
    local names
    names=$(printf ', %s' "${tried[@]}")
    die "can't read $REPO's releases: none of the GitHub accounts gh is signed in to (${names#, }) can read it (or GitHub can't be reached); sign in to one that can (\`gh auth login\`), or set GH_TOKEN"
  fi
}

# can_read <token>: the token can read $REPO (it's passed in the environment, never on a command line).
can_read() { GH_TOKEN=$1 gh api "repos/$REPO" --silent >/dev/null 2>&1; }

# gh signed in (or given GH_TOKEN) is preferred: it reads private repositories.
use_gh() { have gh && gh auth status --hostname github.com >/dev/null 2>&1; }

api() {
  local t
  t=$(token)
  curl -fsSL -H "Accept: application/vnd.github+json" ${t:+-H "Authorization: Bearer $t"} "$API/repos/$REPO/$1" ||
    die "can't read $REPO's releases: a private repository needs a GitHub sign-in (\`gh auth login\`, or GH_TOKEN)"
}

# public_latest_tag: the tag GitHub's latest-release page redirects to
# (…/releases/tag/<tag>; with no releases, it sends the releases page).
public_latest_tag() {
  local location
  location=$(curl -q -sS -o /dev/null -w '%{http_code} %{redirect_url}' "$WEB/$REPO/releases/latest") ||
    die "can't reach GitHub to find $NAME's latest release"
  case $location in
    3??\ */releases/tag/*) echo "${location##*/releases/tag/}" ;;
    3??\ *) ;;
    404\ *) die "can't find $NAME's releases: GitHub has no public repository $REPO" ;;
    *) die "can't read $NAME's latest release from GitHub (HTTP ${location%% *})" ;;
  esac
}

latest_tag() {
  if [ -n "$PUBLIC" ]; then
    public_latest_tag
  elif use_gh; then
    gh release view --repo "$REPO" --json tagName --jq .tagName
  else
    api releases/latest | tr ',' '\n' | sed -n 's/^ *"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1
  fi
}

# download <tag> <file>: a release file into $tmp.
download() {
  local tag=$1 name=$2
  if [ -n "$PUBLIC" ]; then
    # The download URL redirects to where GitHub stores the file.
    local code
    code=$(curl -q -sSL -o "$tmp/$name" -w '%{http_code}' "$WEB/$REPO/releases/download/$tag/$name") ||
      die "can't reach GitHub to download $name"
    case $code in
      200) ;;
      404) no_release "release $tag has no $name" ;;
      *) die "can't download $name from GitHub (HTTP $code)" ;;
    esac
    return
  fi
  if use_gh; then
    gh release download "$tag" --repo "$REPO" --pattern "$name" --dir "$tmp" ||
      no_release "release $tag has no $name"
    return
  fi
  # The API URL serves a private repository's file to a signed-in request.
  local url t
  url=$(api "releases/tags/$tag" | tr ',{' '\n' | awk -v name="$name" '
    /"url": *"[^"]*\/releases\/assets\/[0-9]+"/ { match($0, /https:[^"]*/); url = substr($0, RSTART, RLENGTH) }
    $0 ~ "\"name\": *\"" name "\"" { print url; exit }')
  [ -n "$url" ] || no_release "release $tag has no $name"
  t=$(token)
  curl -fsSL -H "Accept: application/octet-stream" ${t:+-H "Authorization: Bearer $t"} -o "$tmp/$name" "$url"
}

verify_sum() {
  local file=$1 sums=$2 want got
  want=$(awk -v name="$(basename "$file")" '{ n = $2; sub(/^\*/, "", n) } n == name { print $1; exit }' "$sums")
  [ -n "$want" ] || die "$(basename "$sums") doesn't list $(basename "$file")"
  if have sha256sum; then got=$(sha256sum "$file" | cut -d' ' -f1); else got=$(shasum -a 256 "$file" | cut -d' ' -f1); fi
  [ "$got" = "$want" ] || die "$(basename "$file"): checksum mismatch: not installing it"
  say "Checksum OK."
}

# verify_app <app>: its signature seals every file; a release must be TEAM_ID's
# Developer ID signature, notarized. PROCAUTO_INSTALL_UNSIGNED=1 accepts any valid
# signature (testing local builds).
verify_app() {
  local app=$1 team
  codesign --verify --deep --strict "$app" || die "the app's signature doesn't verify: not installing it"
  if [ -z "$TEAM_ID" ] || [ -n "${PROCAUTO_INSTALL_UNSIGNED:-}" ]; then
    say "Signature OK (not checked against a Developer ID)."
    return
  fi
  team=$(codesign -dv --verbose=2 "$app" 2>&1 | sed -n 's/^TeamIdentifier=//p')
  [ "$team" = "$TEAM_ID" ] || die "the app is signed by team ${team:-none}, not $TEAM_ID: not installing it"
  spctl --assess --type execute "$app" || die "Gatekeeper rejects the app (not notarized?): not installing it"
  say "Signature OK (Developer ID $TEAM_ID, notarized)."
}

main "$@"
