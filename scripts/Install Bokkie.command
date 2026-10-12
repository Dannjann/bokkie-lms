#!/bin/sh
# Installs Bokkie on this computer. Double-click it on macOS; run it from a
# terminal on Linux.
#
# Everything careful happens inside the bokkie binary. This file exists to pick
# the right binary for this machine and to ask for the administrator password
# once, rather than leaving someone to work both out at the counter.
set -eu
cd "$(dirname "$0")"

binary=$(sh ./pick-binary.sh) || exit 1

echo
echo "Installing Bokkie from $binary."
echo "You will be asked for this computer's password."
echo

if ! sudo "./$binary" install; then
    echo
    echo "The install did not finish. Nothing has been removed."
    echo "Send the developer everything printed above."
    echo
    exit 1
fi

echo
echo "Bokkie is installed and starts with this computer."
echo
echo "Open this address on any computer or phone at the station:"
echo "    http://bokkie.local:8080"
echo
