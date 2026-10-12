#!/bin/sh
# Upload the release folder to GitHub as a draft.
#
# A draft rather than a release: the windows-latest check in
# .github/workflows/verify-windows.yml installs and uninstalls the real asset
# before anybody can download it, and `releases/latest/download/` — which is
# what the setup exe and the download page both point at — skips drafts. So
# nothing is reachable by a station until a person publishes it.
#
# This runs here and not in CI because signing runs here. The private release
# key never goes to GitHub: a repository secret is still a copy of the one key
# whose loss locks every installed host out of updates forever.
set -eu

cd "$(dirname "$0")/.."

if ! command -v gh >/dev/null 2>&1; then
    echo "publish: the GitHub CLI (gh) is not installed." >&2
    echo "  brew install gh && gh auth login" >&2
    exit 1
fi

TAG=$(git describe --tags --exact-match 2>/dev/null || true)
if [ -z "$TAG" ]; then
    echo "publish: this commit is not an exact version tag, so there is no release to publish." >&2
    echo "  git tag -a v0.1.0 -m 'first release' && make release" >&2
    exit 1
fi

# The signature is what makes the folder a release rather than a pile of
# binaries. Checking it exists here means a half-run `make release` cannot be
# uploaded, and checking it is newer than the binary means a stale signature
# from a previous build cannot either.
if [ ! -f dist/latest.json.sig ]; then
    echo "publish: dist/latest.json.sig is missing — run 'make release' first." >&2
    exit 1
fi
if [ dist/bokkie-win-amd64.exe -nt dist/latest.json.sig ]; then
    echo "publish: the Windows binary is newer than the signature beside it." >&2
    echo "  That means dist/ was rebuilt after it was signed. Run 'make release' again." >&2
    exit 1
fi
if [ ! -f dist/BokkieSetup.exe ]; then
    echo "publish: dist/BokkieSetup.exe is missing — run 'make release' first." >&2
    exit 1
fi

# Refuse to publish a second time rather than quietly making a duplicate.
if gh release view "$TAG" >/dev/null 2>&1; then
    echo "publish: a release for $TAG already exists." >&2
    echo "  gh release view $TAG --web" >&2
    exit 1
fi

NOTES=$(mktemp)
trap 'rm -f "$NOTES"' EXIT
cat > "$NOTES" <<NOTE
Download **BokkieSetup.exe** and double-click it. That is the whole install.

Windows will show "Windows protected your PC" because this file is not signed
by a certificate this project has bought. Choose **More info**, then
**Run anyway**. The downloads page carries the SHA-256 if you want to check the
file instead of trusting that sentence.

The other files are for an offline install from a USB stick, and for macOS and
Linux hosts. See docs/runbook.md.
NOTE

echo "publish: creating draft release $TAG"
gh release create "$TAG" \
    --draft \
    --title "Bokkie $TAG" \
    --notes-file "$NOTES" \
    dist/BokkieSetup.exe \
    dist/bokkie-win-amd64.exe \
    dist/bokkie-mac-arm64 \
    dist/bokkie-mac-amd64 \
    dist/bokkie-linux-amd64 \
    dist/bokkie-linux-arm64 \
    dist/latest.json \
    dist/latest.json.sig \
    "dist/Install Bokkie.cmd" \
    "dist/Install Bokkie.command" \
    "dist/Update Bokkie.cmd" \
    "dist/Update Bokkie.command" \
    dist/pick-binary.sh

# GitHub does not fire release events for drafts, so the check has to be asked
# for by name. Printing "the check is running" without this is how an hour goes
# into the workflow file looking for a run that was never triggered.
echo
echo "publish: starting the Windows check against $TAG"
if gh workflow run verify-windows.yml -f tag="$TAG"; then
    echo "  gh run list --workflow=verify-windows.yml"
else
    echo "  could not start it; run it by hand:" >&2
    echo "  gh workflow run verify-windows.yml -f tag=$TAG" >&2
fi

echo
echo "publish: $TAG is a DRAFT. Nothing can download it yet —"
echo "  releases/latest/download/ skips drafts, which is what the setup exe"
echo "  and the downloads page both point at."
echo
echo "  Publish it once the Windows check is green:"
echo "    gh release edit $TAG --draft=false"
