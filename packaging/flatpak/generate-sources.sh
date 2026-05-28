#!/usr/bin/env bash
# Regenerate the offline Go module sources the Flatpak build consumes:
#   packaging/flatpak/go.mod.yml  — sources that reconstruct vendor/ at fetch time
#   packaging/flatpak/modules.txt — the vendor/modules.txt it copies in
# Re-run whenever go.mod / go.sum change. Requires network + a Go toolchain.
#
# Uses dennwc/flatpak-go-mod, which lists each module as an archive source so
# flatpak-builder downloads them during the (network-allowed) fetch phase and
# the actual build stays offline — a Flathub requirement.
set -euo pipefail

# Pinned for reproducible output; bump deliberately.
tool="github.com/dennwc/flatpak-go-mod@v0.1.0"

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
out_dir="$repo_root/packaging/flatpak"

cd "$repo_root"
echo "Generating ${out_dir}/{go.mod.yml,modules.txt} from go.mod ..."
go run "$tool" -out "$out_dir" .
echo "Done."
