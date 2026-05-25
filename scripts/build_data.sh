#!/usr/bin/env bash
# Refresh the local station dataset from SBB + Wikidata.
#
#   - Downloads the SBB Service Points (Didok) CSV.
#   - Distills into the slim per-station JSON.
#   - Fetches Wikidata multilingual city labels, station altLabels, and
#     IATA codes for the UICs in our index.
#   - Merges everything into data/stations.json (TRAIN-mode only).
#   - Copies the result next to the Go embed.
#
# Idempotent: re-running picks up wherever the Wikidata fetch left off via
# its own on-disk resume.

set -euo pipefail

cd "$(dirname "$0")/.."

SBB_CSV_URL='https://data.sbb.ch/api/explore/v2.1/catalog/datasets/dienststellen-gemass-opentransportdataswiss/exports/csv?lang=en&delimiter=%3B&use_labels=true'
# Annual Swiss timetable GTFS bundle. URL embeds the timetable year + a
# publication date — refresh from https://opentransportdata.swiss/en/cookbook/gtfs/
# when SBB publishes the next fp<YYYY> (usually mid-December).
GTFS_URL=${GTFS_URL:-'https://opentransportdata.swiss/wp-content/uploads/2026/01/gtfs_fp2026_20260126_mit-frequencies_v2.zip'}
TMP_CSV=$(mktemp -t sbb_dienststellen.XXXX.csv)
trap 'rm -f "$TMP_CSV"' EXIT

echo "==> downloading SBB Service Points CSV"
curl -sSfL "$SBB_CSV_URL" -o "$TMP_CSV"

echo "==> distilling stations"
python3 scripts/distill_stations.py "$TMP_CSV" data/stations.json

echo "==> augmenting with foreign train hubs from GTFS (Konstanz, Karlsruhe Hbf, …)"
python3 scripts/fetch_gtfs_stops.py "$GTFS_URL" data/stations.json

echo "==> fetching Wikidata vocab (transit concepts: railway station / airport / …)"
python3 scripts/fetch_wikidata_vocab.py data/wikidata-vocab.json

echo "==> fetching Wikidata per-station enrichment (city labels + altLabels + IATA)"
python3 scripts/fetch_wikidata_aliases.py data/stations.json data/wikidata-aliases.json

echo "==> merging aliases into stations.json"
python3 scripts/merge_wikidata_aliases.py data/stations.json data/wikidata-aliases.json data/wikidata-vocab.json data/stations.json

echo "==> copying to ui/stations/ for the Go embed"
cp data/stations.json ui/stations/stations.json

echo "done."
