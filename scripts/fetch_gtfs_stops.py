#!/usr/bin/env python3
"""Augment data/stations.json with foreign train hubs from the Swiss-wide
GTFS feed.

Why a second source: the SBB Dienststellen dataset is SBB's own internal
infrastructure registry. Foreign stations SBB *serves* (Konstanz, Karlsruhe
Hbf, Frankfurt Main Hbf, Mulhouse, Milano Centrale, Paris-Est, …) appear
there only as ticketing references with `stopPoint=false` and no
`meansOfTransport`, so the distill step drops them. They are first-class
stations in the GTFS feed used by the actual timetable.

This script reads the GTFS `stops.txt` (parent stations only), keeps the
UICs that are not already in `stations.json`, and appends them with
`mode = "TRAIN"`. Heuristic justification: GTFS-only parent stations are
overwhelmingly long-distance train stations served by international
routes — the long-tail BUS / CHAIRLIFT / etc. coverage already lives in
the SBB Dienststellen distill.

GTFS shape (excerpt):
    stop_id              "Parent8014586"       # numeric tail is the UIC
    stop_name            "Konstanz"
    location_type        "1"                   # 1 = parent station
    original_stop_id     "8014586"             # raw UIC / sloid

Run:
    python3 scripts/fetch_gtfs_stops.py <gtfs.zip|url> <stations.json>

The first arg is either a local path to the bundle or a URL to download.
The bundle is ~180 MB; we never extract the large stop_times.txt, only
the ~10 MB stops.txt.

The Swiss timetable bundle is published yearly by SBB at
opentransportdata.swiss. URL format includes the timetable-year date:
    https://opentransportdata.swiss/.../gtfs_fp<YYYY>_<YYYYMMDD>_*.zip
See https://opentransportdata.swiss/en/cookbook/gtfs/ for the current
download link.
"""

from __future__ import annotations

import csv
import io
import json
import os
import sys
import urllib.request
import zipfile
from collections import Counter

# Default mode assigned to GTFS-only stations. See module docstring.
DEFAULT_MODE = "TRAIN"


def open_bundle(source: str) -> bytes:
    """Return the GTFS zip bytes from a local path or HTTP URL."""
    if source.startswith(("http://", "https://")):
        print(f"  downloading {source} (~180 MB) ...", file=sys.stderr, flush=True)
        req = urllib.request.Request(source, headers={"User-Agent": "sbb-tui-spike/0.1"})
        with urllib.request.urlopen(req, timeout=120) as resp:
            return resp.read()
    if not os.path.exists(source):
        raise FileNotFoundError(source)
    with open(source, "rb") as f:
        return f.read()


def extract_parent_stations(zip_bytes: bytes) -> list[dict]:
    """Stream-extract stops.txt from the zip; return parent stations
    (location_type == "1") as dicts."""
    z = zipfile.ZipFile(io.BytesIO(zip_bytes))
    with z.open("stops.txt") as raw:
        text = io.TextIOWrapper(raw, encoding="utf-8-sig", newline="")
        reader = csv.DictReader(text)
        return [r for r in reader if r.get("location_type") == "1"]


def uic_from_stop_id(stop_id: str) -> str:
    """Numeric portion of the GTFS parent-station id. The feed prefixes
    parent stations with "Parent" (e.g. "Parent8014586"); the rest is the
    UIC matching Wikidata's wdt:P722."""
    return stop_id.removeprefix("Parent").strip()


def main(source: str, stations_path: str) -> None:
    with open(stations_path, encoding="utf-8") as f:
        stations = json.load(f)
    existing_uics = {s.get("uic") for s in stations if s.get("uic")}
    print(f"  existing index has {len(existing_uics)} UICs", file=sys.stderr)

    print("  reading GTFS bundle ...", file=sys.stderr, flush=True)
    zip_bytes = open_bundle(source)
    parents = extract_parent_stations(zip_bytes)
    print(f"  GTFS parent stations: {len(parents)}", file=sys.stderr)

    new_rows: list[dict] = []
    skipped = 0
    for r in parents:
        uic = uic_from_stop_id(r["stop_id"])
        if not uic or not uic.isdigit():
            skipped += 1
            continue
        if uic in existing_uics:
            continue
        name = r.get("stop_name", "").strip()
        if not name:
            continue
        new_rows.append({"uic": uic, "name": name, "mode": DEFAULT_MODE})

    if not new_rows:
        print("  nothing to add — all GTFS UICs already in stations.json", file=sys.stderr)
        return

    # Sort the merged set by UIC so output stays deterministic across runs.
    merged = sorted(stations + new_rows, key=lambda s: s.get("uic", ""))
    with open(stations_path, "w", encoding="utf-8") as f:
        json.dump(merged, f, ensure_ascii=False, separators=(",", ":"))

    by_prefix = Counter(r["uic"][:2] for r in new_rows)
    print(f"  appended {len(new_rows)} GTFS-only stations to {stations_path}", file=sys.stderr)
    print("  by UIC country prefix:", file=sys.stderr)
    for k, v in sorted(by_prefix.items(), key=lambda kv: -kv[1]):
        print(f"    {k}xxxxx  {v:>4}", file=sys.stderr)
    if skipped:
        print(f"  skipped {skipped} rows with non-numeric stop_id", file=sys.stderr)


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("usage: fetch_gtfs_stops.py <gtfs.zip|url> <stations.json>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1], sys.argv[2])
