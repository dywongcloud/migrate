#!/bin/bash
# Vendors dywongcloud/agentos's holoiroh-vendor tree (the user's own pre-solved
# patches for iroh-live's unpublished-crate friction: broadened VideoToolbox
# cfg gates in iroh-live-patched, and a build.rs fix in openh264-rs-patched)
# as a SIBLING of gateway/, matching the relative paths the [patch] block in
# gateway/Cargo.toml expects (../holoiroh-vendor/iroh-live-patched/*,
# ../holoiroh-vendor/openh264-rs-patched/openh264-sys2). Idempotent: does
# nothing if holoiroh-vendor/iroh-live-patched already exists.
#
# AGENTOS_REV pins the exact agentos commit vendored; override to update.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/holoiroh-vendor"
AGENTOS_REV="${AGENTOS_REV:-f6f9eb4ca460e58bb8804337f757519b39f22880}"

if [ -d "$DEST/iroh-live-patched" ] && [ -d "$DEST/openh264-rs-patched" ]; then
  echo "holoiroh-vendor already present at $DEST, skipping (rm -rf to refetch)"
  exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "cloning dywongcloud/agentos@$AGENTOS_REV (sparse: holoiroh-vendor/ only)"
git clone --quiet --filter=blob:none --sparse --no-checkout \
  https://github.com/dywongcloud/agentos.git "$TMP/agentos"
(
  cd "$TMP/agentos"
  git sparse-checkout set holoiroh-vendor
  git checkout --quiet "$AGENTOS_REV"
)

rm -rf "$DEST"
mv "$TMP/agentos/holoiroh-vendor" "$DEST"
echo "vendored: $DEST (iroh-live-patched, openh264-rs-patched)"
