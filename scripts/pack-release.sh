#!/usr/bin/env bash
# Build clean Linux release tarballs (no macOS AppleDouble / xattrs).
# Usage: VERSION=v0.4.4 ./scripts/pack-release.sh
set -euo pipefail
VERSION="${VERSION:-v0.4.4}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${ROOT}/dist/release"
export COPYFILE_DISABLE=1
export COPY_EXTENDED_ATTRIBUTES_DISABLE=1

cd "$ROOT"
if [[ ! -d web/dist/assets ]]; then
  echo "build web first: (cd web && npm run build)" >&2
  exit 1
fi
rm -rf "$OUT/build"
mkdir -p "$OUT/build"

build_one() {
  local goos=$1 goarch=$2
  local name="mieru-panel-${VERSION}-${goos}-${goarch}"
  local dir="$OUT/build/${name}"
  mkdir -p "$dir"
  echo "==> ${name}"
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -ldflags="-s -w -X main.Version=${VERSION}" -o "${dir}/panel" ./cmd/panel
  CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch go build -ldflags="-s -w -X main.Version=${VERSION}" -o "${dir}/agent" ./cmd/agent
  xattr -cr "$dir" 2>/dev/null || true
  find "$dir" -name '._*' -delete 2>/dev/null || true
  python3 - "$dir" "$name" "$OUT/${name}.tar.gz" <<'PY'
import sys, tarfile
from pathlib import Path
src, name, out = Path(sys.argv[1]), sys.argv[2], Path(sys.argv[3])
with tarfile.open(out, "w:gz", format=tarfile.GNU_FORMAT) as tar:
    tar.add(src, arcname=name, filter=lambda ti: None if Path(ti.name).name.startswith("._") else ti)
with tarfile.open(out) as tar:
    names = tar.getnames()
assert any(n.endswith("/panel") for n in names), names
assert not any("._" in n for n in names), names
print("  ", out, out.stat().st_size, "bytes", names)
PY
}
build_one linux amd64
build_one linux arm64
echo "OK: $OUT"
