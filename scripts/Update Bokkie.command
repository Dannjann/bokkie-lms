#!/bin/sh
# Updates Bokkie on this computer to the version in this folder.
#
# The binary does the work: it checks the signature, backs the database up,
# stops the service, swaps itself in, starts again, and puts the old version
# back if the new one does not answer.
set -eu
cd "$(dirname "$0")"
here=$(pwd)

if [ ! -f latest.json ]; then
    echo
    echo "This folder does not look like a Bokkie release: latest.json is missing."
    echo
    exit 1
fi

binary=$(sh ./pick-binary.sh) || exit 1

echo
echo "Checking what is in this folder..."
echo
if ! "./$binary" update -check "$here"; then
    echo
    echo "This folder is not a genuine Bokkie release, or it is damaged."
    echo "Nothing on this computer has been changed."
    echo
    exit 1
fi

echo
printf "Install this version? The database is backed up first. [y/N] "
read -r confirm
case "$confirm" in
    y|Y) ;;
    *) echo "Nothing was changed."; exit 0 ;;
esac

echo
echo "You will be asked for this computer's password."
echo
if ! sudo "./$binary" update "$here"; then
    echo
    echo "The update did not finish. Read the message above: if it says the"
    echo "previous version was put back, the station is working normally and"
    echo "this can wait for the developer."
    echo
    exit 1
fi
