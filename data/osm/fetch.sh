#!/usr/bin/env bash
# Download a city OSM extract for MetroSim. Default is San Francisco (~30MB).
# Override OSM_EXTRACT_URL and OSM_OUT_FILE to fetch a different city.
set -euo pipefail

OUT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
URL="${OSM_EXTRACT_URL:-https://download.bbbike.org/osm/bbbike/SanFrancisco/SanFrancisco.osm.pbf}"
OUT="${OSM_OUT_FILE:-$OUT_DIR/city.osm.pbf}"

if [[ -f "$OUT" ]]; then
    echo "$OUT already exists (size: $(du -h "$OUT" | cut -f1)). Delete to refetch."
    exit 0
fi

echo "Fetching $URL ..."
curl -L --fail --progress-bar -o "$OUT.tmp" "$URL"
mv "$OUT.tmp" "$OUT"
echo "Wrote $OUT ($(du -h "$OUT" | cut -f1))"
