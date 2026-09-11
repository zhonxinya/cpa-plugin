#!/usr/bin/env bash
# Build, package, and self-verify one CPA plugin archive.
#
# Shared by the GitHub and Gitea release workflows so both hosts produce the
# same layout rules: the ZIP root holds exactly one library, named
# opencode-go.<ext> for the target platform.
#
# Usage (from anywhere):
#   GOOS=linux GOARCH=amd64 CC=gcc ./scripts/build-archives.sh
#   OUT_DIR=/tmp/dist GOOS=darwin GOARCH=arm64 ./scripts/build-archives.sh
set -euo pipefail

plugin_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' <"$plugin_dir/VERSION")"
if [ -z "$version" ]; then
  echo "VERSION file is empty" >&2
  exit 1
fi

goos="${GOOS:-$(go env GOOS)}"
goarch="${GOARCH:-$(go env GOARCH)}"

# Resolve the output directory to an absolute path before changing directory.
out_dir="${OUT_DIR:-$plugin_dir/dist}"
case "$out_dir" in
/*) ;;
*) out_dir="$PWD/$out_dir" ;;
esac
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"

# The plugin loader picks the library by platform extension, so the name inside
# the archive must follow the target GOOS rather than the build host.
case "$goos" in
darwin) ext=dylib ;;
windows) ext=dll ;;
*) ext=so ;;
esac

# Resolve the ldflags up front: a temporary assignment on the build line is not
# visible to expansions elsewhere on that same line, which set -u would reject.
go_ldflags="${GO_LDFLAGS:-}"
if [ -z "$go_ldflags" ]; then
  go_ldflags="-s -w"
fi

archive="$out_dir/opencode-go_${version}_${goos}_${goarch}.zip"
library="$out_dir/opencode-go.${ext}"
rm -f "$archive" "$archive.sha256" "$library" "$out_dir/opencode-go.h"

# go resolves modules from the working directory, so build from the plugin root.
cd "$plugin_dir"
CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
  go build -trimpath -buildmode=c-shared -ldflags "$go_ldflags" -o "$library" .
rm -f "$out_dir/opencode-go.h"

go run ./scripts/package-release.go \
  -library "$library" \
  -archive "$archive" \
  -checksum "$archive.sha256"

python3 ./scripts/verify_release_assets.py \
  --archive "$archive" \
  --sha256 "$archive.sha256"

echo "$archive"
