#!/usr/bin/env bash
# capture-post-refactor.sh — run after Phase 2.5 Slice 3 has landed
# (usePixelWalk flag flipped on by default). Captures pixel-walk
# framebuffer goldens into testdata/phase2.5/post/ and diffs them
# against the pre-Slice-3 baseline from capture-pre-refactor.sh.
#
# Emits a per-frame pass/diff summary. Non-zero exit if any frame
# drifted; this is an audit tool, not a CI gate, so the caller
# decides whether drift is acceptable (intentional for Slice 3's
# priority-inversion fix, unexpected for Slice 1/2).
#
# Must be run with the same ROM set that produced pre/fb_goldens.json.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

pre="$repo_root/testdata/phase2.5/pre/fb_goldens.json"
post_dir="$repo_root/testdata/phase2.5/post"
mkdir -p "$post_dir"

if [[ ! -f "$pre" ]]; then
  echo "!! missing $pre; run capture-pre-refactor.sh first"
  exit 2
fi

UPDATE_FB_GOLDENS=1 go test ./internal/parity/ -run TestFramebufferHashGoldens -v || {
  echo "!! fb_hash capture failed under usePixelWalk"
  exit 1
}

src="$repo_root/internal/parity/testdata/fb_goldens.json"
cp "$src" "$post_dir/fb_goldens.json"

echo
echo "=== Diff: pre vs post ==="
if diff -q "$pre" "$post_dir/fb_goldens.json" >/dev/null; then
  echo "No drift. Framebuffers identical across pre/post refactor."
  exit 0
fi

diff "$pre" "$post_dir/fb_goldens.json" || true
echo
echo "Drift detected. Review per-frame deltas above; intentional drift"
echo "from Slice 3 (BG3 priority inversion, pixel-granular OPT) is OK,"
echo "other drift should be investigated before Slice 4."
exit 1
