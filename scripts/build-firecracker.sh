#!/bin/bash
set -euxo pipefail
SRC=~/fc/firecracker-next
ARCH_TARGET="$1"   # x86_64-unknown-linux-gnu | aarch64-unknown-linux-gnu
OUTNAME="$2"
JOBS="${3:-2}"

if [ ! -x ~/.cargo/bin/cargo ]; then
  curl -sSf --proto '=https' --tlsv1.2 https://sh.rustup.rs -o /tmp/rustup-init.sh
  sh /tmp/rustup-init.sh -y --default-toolchain stable --profile minimal
fi
source ~/.cargo/env
rustup target add "$ARCH_TARGET" || true

cd "$SRC"
CARGO_TERM_PROGRESS_WHEN=never cargo build --release --target "$ARCH_TARGET" \
  --package firecracker --package jailer -j "$JOBS"
OUT=/Users/dylanwong/daedal/kernel/build
mkdir -p "$OUT"
cp "build/cargo_target/$ARCH_TARGET/release/firecracker" "$OUT/$OUTNAME"
"$OUT/$OUTNAME" --version | head -1
file "$OUT/$OUTNAME"
echo FC_BUILD_DONE_$OUTNAME
