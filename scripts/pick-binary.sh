#!/bin/sh
# Prints the name of the binary in this folder that runs on this machine.
#
# Five binaries ship together so one release folder works on whichever host the
# station ends up using. Choosing between them is exactly the kind of decision
# that should never reach the person holding the USB stick.
set -eu

case "$(uname -s)" in
    Darwin) os=mac ;;
    Linux)  os=linux ;;
    *)
        echo "This script does not know how to install on $(uname -s)." >&2
        echo "On Windows, double-click 'Install Bokkie.cmd' instead." >&2
        exit 1
        ;;
esac

case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64)  arch=amd64 ;;
    *)
        echo "There is no bokkie build for $(uname -m)." >&2
        exit 1
        ;;
esac

binary="bokkie-$os-$arch"
if [ ! -f "$binary" ]; then
    echo "Could not find $binary in this folder." >&2
    echo "Copy the whole Bokkie folder off the USB stick and run this again." >&2
    exit 1
fi

# A binary that arrived by USB or download is not executable and, on macOS, is
# quarantined. Both are cleared here rather than in an instruction nobody reads.
chmod +x "$binary" 2>/dev/null || true
xattr -d com.apple.quarantine "$binary" 2>/dev/null || true

echo "$binary"
