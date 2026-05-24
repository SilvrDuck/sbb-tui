#!/usr/bin/env python3
"""Distill the SBB Service Points (Didok) CSV into a slim JSON used by the
fuzzy station picker.

Source: https://data.sbb.ch/explore/dataset/dienststellen-gemass-opentransportdataswiss/
Refresh: daily at 06:00 Europe/Zurich.

Run:
    curl -sS 'https://data.sbb.ch/api/explore/v2.1/catalog/datasets/dienststellen-gemass-opentransportdataswiss/exports/csv?lang=en&delimiter=%3B&use_labels=true' -o /tmp/sbb_dienststellen.csv
    python3 scripts/distill_stations.py /tmp/sbb_dienststellen.csv data/stations.json

Output schema (array of objects):
    {
        "uic": "8501008",      # stable join key, matches transport.opendata.ch /v1/locations id
        "name": "Genève",      # designationOfficial
        "abbr": "GE",          # SBB code, when present — used as a free alias
        "mode": "TRAIN"        # primary mode (first segment if pipe-separated)
    }
"""

import csv
import json
import sys
from pathlib import Path

# Modes we surface in the picker. Excludes empty and "UNKNOWN".
ALLOWED_MODES = {
    "TRAIN",
    "METRO",
    "TRAM",
    "BUS",
    "BOAT",
    "CABLE_CAR",
    "CABLE_RAILWAY",
    "RACK_RAILWAY",
    "CHAIRLIFT",
    "ELEVATOR",
}


def primary_mode(raw: str) -> str | None:
    """Return the first allowed mode in a pipe-separated string, or None."""
    if not raw or raw == "UNKNOWN":
        return None
    for part in raw.split("|"):
        if part in ALLOWED_MODES:
            return part
    return None


def main(csv_path: str, json_path: str) -> None:
    # utf-8-sig strips the BOM that prefixes the first header field.
    with open(csv_path, newline="", encoding="utf-8-sig") as f:
        reader = csv.DictReader(f, delimiter=";")
        seen: dict[str, dict] = {}
        for row in reader:
            if row.get("stopPoint") != "true":
                continue
            if row.get("validTo") != "9999-12-31":
                continue  # historical / future-only rows
            mode = primary_mode(row.get("meansOfTransport", ""))
            if mode is None:
                continue
            uic = row.get("number", "").strip()
            name = row.get("designationOfficial", "").strip()
            if not uic or not name:
                continue
            # If a UIC appears more than once for the current validity window,
            # keep the first — they should be identical in practice.
            if uic in seen:
                continue
            entry = {"uic": uic, "name": name, "mode": mode}
            abbr = row.get("abbreviation", "").strip()
            if abbr:
                entry["abbr"] = abbr
            seen[uic] = entry

    stations = sorted(seen.values(), key=lambda s: s["uic"])

    Path(json_path).parent.mkdir(parents=True, exist_ok=True)
    with open(json_path, "w", encoding="utf-8") as f:
        json.dump(stations, f, ensure_ascii=False, separators=(",", ":"))

    modes: dict[str, int] = {}
    for s in stations:
        modes[s["mode"]] = modes.get(s["mode"], 0) + 1
    print(f"wrote {len(stations)} stations to {json_path}")
    print("by mode:")
    for m, c in sorted(modes.items(), key=lambda kv: -kv[1]):
        print(f"  {m:<16} {c:>6}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("usage: distill_stations.py <input.csv> <output.json>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1], sys.argv[2])
