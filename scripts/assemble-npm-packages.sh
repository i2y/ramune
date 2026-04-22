#!/usr/bin/env bash
# Assembles the npm packages that ship the Ramune CLI.
#
# Produces, under $OUT_DIR:
#   ramune/                       top-level shim package (version stamped,
#                                   optionalDependencies rewritten, "private"
#                                   flag stripped)
#   platform/<key>/               one package per supported OS/CPU combo,
#                                   each containing bin/ramune{,-toolchain}[.exe]
#
# Inputs:
#   VERSION        version string (e.g. "0.16.0"). Conventionally the git tag
#                  with the leading "v" stripped.
#   ARTIFACTS_DIR  directory containing per-platform build output subdirectories:
#                    darwin-arm64/  linux-x64/     linux-arm64/
#                    win32-x64/     win32-arm64/
#                  Each subdir must contain `ramune` and `ramune-toolchain`
#                  (append `.exe` on Windows targets).
#   OUT_DIR        destination for the assembled packages.
#
# After this script succeeds, each assembled package directory is ready for
# `npm publish --access public`.

set -euo pipefail

: "${VERSION:?VERSION must be set (e.g. VERSION=0.16.0)}"
: "${ARTIFACTS_DIR:?ARTIFACTS_DIR must be set}"
: "${OUT_DIR:?OUT_DIR must be set}"

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"

PLATFORMS=(
  # key            npm os   npm cpu
  "darwin-arm64    darwin   arm64"
  "linux-x64       linux    x64"
  "linux-arm64     linux    arm64"
  "win32-x64       win32    x64"
  "win32-arm64     win32    arm64"
)

mkdir -p "$OUT_DIR/platform"

for row in "${PLATFORMS[@]}"; do
  # shellcheck disable=SC2086
  set -- $row
  key="$1"; pkg_os="$2"; pkg_cpu="$3"
  pkg_dir="$OUT_DIR/platform/$key"
  src_dir="$ARTIFACTS_DIR/$key"

  if [[ ! -d "$src_dir" ]]; then
    echo "ERROR: missing artifact dir $src_dir" >&2
    exit 1
  fi

  rm -rf "$pkg_dir"
  mkdir -p "$pkg_dir/bin"

  if [[ "$pkg_os" == "win32" ]]; then
    cp "$src_dir/ramune.exe"           "$pkg_dir/bin/ramune.exe"
    cp "$src_dir/ramune-toolchain.exe" "$pkg_dir/bin/ramune-toolchain.exe"
  else
    cp "$src_dir/ramune"           "$pkg_dir/bin/ramune"
    cp "$src_dir/ramune-toolchain" "$pkg_dir/bin/ramune-toolchain"
    chmod 0755 "$pkg_dir/bin/ramune" "$pkg_dir/bin/ramune-toolchain"
  fi

  sed \
    -e "s/__PLATFORM__/$key/g" \
    -e "s/__VERSION__/$VERSION/g" \
    -e "s/__OS__/$pkg_os/g" \
    -e "s/__CPU__/$pkg_cpu/g" \
    "$ROOT/npm/platform/template.package.json" \
    > "$pkg_dir/package.json"

  cp "$ROOT/LICENSE"                "$pkg_dir/LICENSE"
  cp "$ROOT/npm/platform/README.md" "$pkg_dir/README.md"

  echo "assembled platform package: $pkg_dir"
done

top_dir="$OUT_DIR/ramune"
rm -rf "$top_dir"
mkdir -p "$top_dir/bin"

cp "$ROOT/npm/ramune/bin/ramune.js" "$top_dir/bin/ramune.js"
chmod 0755 "$top_dir/bin/ramune.js"
cp "$ROOT/npm/ramune/README.md" "$top_dir/README.md"
cp "$ROOT/LICENSE"              "$top_dir/LICENSE"

node - "$ROOT/npm/ramune/package.json" "$top_dir/package.json" "$VERSION" <<'NODE'
const fs = require("node:fs");
const [src, dst, version] = process.argv.slice(2);
const pkg = JSON.parse(fs.readFileSync(src, "utf8"));
pkg.version = version;
if (pkg.optionalDependencies) {
  for (const name of Object.keys(pkg.optionalDependencies)) {
    pkg.optionalDependencies[name] = version;
  }
}
delete pkg.private;
fs.writeFileSync(dst, JSON.stringify(pkg, null, 2) + "\n");
NODE

echo "assembled top-level shim:   $top_dir"
echo "done. version=$VERSION out=$OUT_DIR"
