#!/bin/sh
# Build this working tree and put it on this machine's bokkie service.
#
# This is the developer's loop, not the station's. The station gets a signed
# release folder and double-clicks "Install Bokkie.command"; this script builds
# from source and skips the signature, which is exactly why it must never be
# staged into a release. See the explicit copy list in the Makefile's
# stage-scripts target.
#
# It exists because the signed path is unavailable until the release key is
# generated: with `PublicKey` empty, `bokkie update` refuses every release by
# design, and the alternative its own error text names is "install the update by
# hand". This is that, done the same way twice.
#
# Almost nothing careful happens here. `bokkie install` is idempotent and is
# already the whole update: it clears the previous registration, copies itself
# to /usr/local/bin/bokkie, grants the service account access, re-registers,
# opens the firewall, starts, and confirms /healthz answers before returning. It
# also reads the port and data directory from the config already on this
# machine, so an existing install keeps its port.
#
# What this adds is the one thing `install` deliberately does not do: `install`
# has no rollback -- placeExecutable deletes the binary it displaces -- so
# against a database with real rows in it, a backup and a keepable copy of the
# outgoing binary are taken first.
#
#   scripts/dev-install.sh                 build the SPA, build the binary, install
#   scripts/dev-install.sh --skip-build    install whatever is already in dist/
#   scripts/dev-install.sh --dry-run       say what would happen, change nothing
set -eu

cd "$(dirname "$0")/.."
repo=$(pwd)

skip_build=no
dry_run=no
for arg in "$@"; do
    case "$arg" in
        --skip-build) skip_build=yes ;;
        --dry-run)    dry_run=yes ;;
        # The header comment is the help text. Printed by walking it rather
        # than by line number, so editing it above cannot silently start
        # printing shell code.
        -h|--help)
            awk 'NR > 1 && /^#/ { sub(/^# ?/, ""); print; next } NR > 1 { exit }' "$0"
            exit 0
            ;;
        *)
            echo "unknown option: $arg" >&2
            echo "usage: scripts/dev-install.sh [--skip-build] [--dry-run]" >&2
            exit 2
            ;;
    esac
done

# ---------------------------------------------------------------- this machine

case "$(uname -s)" in
    Darwin) os=mac;   goos=darwin; account=_bokkie ;;
    Linux)  os=linux; goos=linux;  account=bokkie ;;
    *)
        echo "dev-install only runs on macOS and Linux." >&2
        echo "On Windows, build with 'make build-all' and run 'bokkie install' from an" >&2
        echo "administrator prompt." >&2
        exit 1
        ;;
esac

case "$(uname -m)" in
    arm64|aarch64) arch=arm64; goarch=arm64 ;;
    x86_64|amd64)  arch=amd64; goarch=amd64 ;;
    *) echo "there is no bokkie build for $(uname -m)." >&2; exit 1 ;;
esac

binary="$repo/dist/bokkie-$os-$arch"
installed="/usr/local/bin/bokkie"

# ---------------------------------------------------------------------- build

version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
commit=$(git rev-parse --short HEAD 2>/dev/null || echo unknown)
date=$(date -u +%Y-%m-%dT%H:%M:%SZ)
pkg=github.com/Dannjann/bokkie-lms/internal/buildinfo

if [ "$skip_build" = yes ]; then
    [ -x "$binary" ] || { echo "--skip-build, but there is no binary at $binary" >&2; exit 1; }
    echo "==> using the existing $binary"
    version=$("$binary" version 2>/dev/null || echo "unknown")
    echo "    $version"
elif [ "$dry_run" = yes ]; then
    echo "==> would build the SPA and $binary at version $version"
else
    echo "==> building the SPA"
    make web-build

    echo "==> building bokkie-$os-$arch at $version"
    mkdir -p "$repo/dist"
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
        -ldflags "-s -w -X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Date=$date" \
        -o "$binary" ./cmd/bokkie
fi

# A build from a dirty tree installs fine and is labelled honestly, but the
# version on the dashboard then names no commit anyone can check out. Worth
# saying once, not worth refusing over.
case "$version" in
    *-dirty|*dirty*)
        echo
        echo "    note: the working tree is dirty, so this build is labelled '$version'"
        echo "    and the dashboard's Build row will not name a reproducible commit."
        echo
        ;;
esac

# ------------------------------------------------------------ existing install

if [ -x "$installed" ]; then
    mode=update
    echo "==> found an existing install at $installed"
    "$installed" version 2>/dev/null | sed 's/^/    now: /' || true
else
    mode=install
    echo "==> no existing install at $installed — this will be a first install"
fi

if [ "$dry_run" = yes ]; then
    echo
    echo "would, as $mode:"
    [ "$mode" = update ] && echo "  - back the database up, and keep the outgoing binary"
    echo "  - sudo $binary install"
    echo
    echo "nothing was changed."
    exit 0
fi

# ------------------------------------------------------------------ safety net

keep="$repo/dist/bokkie-previous"

if [ "$mode" = update ]; then
    # The backup goes where the service already puts them, written as the
    # service account so the nightly retention sweep can still clean it up. A
    # root-owned file in that folder is one it would have to skip forever.
    echo "==> backing up the database before touching anything"
    if ! sudo -u "$account" "$installed" backup; then
        echo
        echo "The backup did not run, so nothing has been changed." >&2
        echo "Fix that before installing over a live database." >&2
        exit 1
    fi

    # `install` deletes the binary it displaces, so the only copy of the version
    # currently working is the one taken here.
    echo "==> keeping the outgoing binary at $keep"
    sudo cp -p "$installed" "$keep"
    sudo chown "$(id -u):$(id -g)" "$keep"
fi

# --------------------------------------------------------------------- install

echo "==> installing (you will be asked for this computer's password)"
if ! sudo "$binary" install; then
    echo
    echo "The install did not finish." >&2
    if [ "$mode" = update ]; then
        echo "The previous binary is at $keep. To put it back:" >&2
        echo "  sudo $keep install" >&2
    fi
    exit 1
fi

# ---------------------------------------------------------------------- report

port=$(sed -n 's/^port:[[:space:]]*//p' "/Library/Application Support/Bokkie/config.yaml" 2>/dev/null \
    || sed -n 's/^port:[[:space:]]*//p' "/var/lib/bokkie/config.yaml" 2>/dev/null \
    || true)
port=${port:-8080}

echo
echo "bokkie is installed and running:"
"$installed" version | sed 's/^/    /'
echo
echo "    http://bokkie.local:$port"
echo "    http://localhost:$port"
echo
if [ "$mode" = update ]; then
    echo "to go back to the previous version:"
    echo "    sudo $keep install"
    echo
fi
