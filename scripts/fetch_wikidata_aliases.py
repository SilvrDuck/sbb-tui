#!/usr/bin/env python3
"""Fetch multilingual city/municipality labels for SBB stations from
Wikidata, joined via the UIC station code property (P722).

The link is structural at every step:
    local UIC  →  Wikidata station (P722)  →  Wikidata city (P131)  →  labels

No string-name matching between our index and Wikidata. Stations whose
Wikidata entry lacks P722 or P131 silently get no aliases.

Run:
    python3 scripts/fetch_wikidata_aliases.py data/stations.json data/wikidata-aliases.json

Output shape (JSON, keyed by UIC):
    {
      "8501008": {
        "de": "Genf",
        "fr": "Genève",
        "it": "Ginevra",
        "en": "Geneva",
        "rm": "Genevra"
      },
      ...
    }

We batch by UIC chunks (VALUES clause) rather than by country prefix:
the global Wikidata query for "all stations with P722 starting 80…"
times out because Germany alone has tens of thousands of P722-tagged
stations, most of which aren't relevant to SBB.
"""

import json
import sys
import time
from urllib.parse import urlencode
from urllib.request import Request, urlopen

ENDPOINT = "https://query.wikidata.org/sparql"
USER_AGENT = "sbb-tui-spike/0.1 (https://github.com/Necrom4/sbb-tui)"

BATCH_SIZE = 250  # UICs per SPARQL query; smaller = fewer 502s from WDQS
SLEEP_BETWEEN = 0.5  # seconds, be polite to the public endpoint
MAX_RETRIES = 4  # per batch, with exponential backoff
BACKOFF_BASE = 2.0  # seconds, doubled per retry

QUERY_TMPL = """
SELECT ?uic ?city ?cityParent ?labelDe ?labelFr ?labelIt ?labelEn ?labelRm ?altLabel ?iata WHERE {{
  VALUES ?uic {{ {values} }}
  ?station wdt:P722 ?uic .
  OPTIONAL {{
    # Return every P131 city for the station along with its own parent
    # (?cityParent). Python disambiguates: when a station has multiple
    # P131 entries (municipality + canton, for instance), we pick the
    # "deepest" one — the city whose parent is also in the station's
    # P131 set. Purely structural via Wikidata's own admin hierarchy.
    ?station wdt:P131 ?city .
    OPTIONAL {{ ?city wdt:P131 ?cityParent }}
    OPTIONAL {{ ?city rdfs:label ?labelDe FILTER(LANG(?labelDe)="de") }}
    OPTIONAL {{ ?city rdfs:label ?labelFr FILTER(LANG(?labelFr)="fr") }}
    OPTIONAL {{ ?city rdfs:label ?labelIt FILTER(LANG(?labelIt)="it") }}
    OPTIONAL {{ ?city rdfs:label ?labelEn FILTER(LANG(?labelEn)="en") }}
    OPTIONAL {{ ?city rdfs:label ?labelRm FILTER(LANG(?labelRm)="rm") }}
  }}
  OPTIONAL {{
    ?station skos:altLabel ?altLabel .
    FILTER(LANG(?altLabel) IN ("de","fr","it","en","rm"))
  }}
  OPTIONAL {{
    # IATA code via airport rail station class + "named after" linkage.
    # Audited 2026-05-24 with a live Wikidata probe — the correct
    # traversal is P138 (named after), not P276 (location) or P361
    # (part of). Q1335652 is "airport railway station". Across all SBB
    # UICs this matches exactly two entries: Geneva (GVA) and Zurich
    # (ZRH), which is the complete set of Swiss SBB airport stations.
    ?station wdt:P31 wd:Q1335652 .
    ?station wdt:P138 ?airport .
    ?airport wdt:P238 ?iata .
  }}
}}
"""


def query_batch(uics: list[str]) -> list[dict]:
    """Run the SPARQL query for one batch of UICs with retry+backoff
    on transient errors. POST avoids the HTTP 431 from GET-with-long-URL."""
    values = " ".join(f'"{u}"' for u in uics)
    body = urlencode({"query": QUERY_TMPL.format(values=values)}).encode("utf-8")
    last_err: Exception | None = None
    for attempt in range(MAX_RETRIES):
        try:
            req = Request(
                ENDPOINT,
                data=body,
                headers={
                    "Accept": "application/sparql-results+json",
                    "User-Agent": USER_AGENT,
                    "Content-Type": "application/x-www-form-urlencoded",
                },
            )
            with urlopen(req, timeout=60) as resp:
                data = json.load(resp)
            return data["results"]["bindings"]
        except Exception as e:
            last_err = e
            sleep_for = BACKOFF_BASE * (2 ** attempt)
            print(f"    batch error ({e}); retrying in {sleep_for:.1f}s [attempt {attempt+1}/{MAX_RETRIES}]", file=sys.stderr)
            time.sleep(sleep_for)
    print(f"    batch failed after {MAX_RETRIES} retries: {last_err}", file=sys.stderr)
    return []


def chunks(seq: list, size: int):
    for i in range(0, len(seq), size):
        yield seq[i : i + size]


def save(out_path: str, by_uic: dict) -> None:
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(by_uic, f, ensure_ascii=False, indent=2, sort_keys=True)


def main(stations_path: str, out_path: str) -> None:
    with open(stations_path, encoding="utf-8") as f:
        stations = json.load(f)
    # Only query proper UICs (country prefix 7 or 8 — internationally
    # registered stations). SBB also assigns 7-digit internal IDs to bus
    # stops starting with 1-6; those are never in Wikidata as UIC-coded
    # entities, so querying them just wastes round-trips.
    uics = sorted(
        {
            s["uic"]
            for s in stations
            if s.get("uic")
            and len(s["uic"]) == 7
            and s["uic"][0] in "78"
        }
    )

    # Resume: if the output file already has entries, skip UICs we've
    # already resolved. Lets a 502 mid-run be recovered just by re-running.
    # Entries are dicts with: language labels (str), "alts" (list[str]),
    # "iata" (str). Loose typing because pyright dislikes mixed value types.
    by_uic: dict = {}
    try:
        with open(out_path, encoding="utf-8") as f:
            by_uic = json.load(f)
        print(f"resuming with {len(by_uic)} already-resolved UICs", file=sys.stderr)
    except FileNotFoundError:
        pass

    pending = [u for u in uics if u not in by_uic]
    print(f"querying Wikidata for {len(pending)} UICs in batches of {BATCH_SIZE}...", file=sys.stderr)

    n_batches = (len(pending) + BATCH_SIZE - 1) // BATCH_SIZE
    for batch_idx, batch in enumerate(chunks(pending, BATCH_SIZE)):
        bindings = query_batch(batch)
        # Mark every UIC in this batch as "checked" (empty dict) even if no
        # bindings came back, so resume doesn't re-query them.
        for u in batch:
            by_uic.setdefault(u, {})
        # Stage 1: bucket by (uic, city) so each city's label set is kept
        # consistent — never mixed with another city's labels.
        for b in bindings:
            uic = b["uic"]["value"]
            entry = by_uic.setdefault(uic, {})
            entry.setdefault("alts", [])
            entry.setdefault("iata", "")
            entry.setdefault("_cityCandidates", {})
            if "altLabel" in b:
                alt = b["altLabel"]["value"]
                if alt and alt not in entry["alts"]:
                    entry["alts"].append(alt)
            if "iata" in b and not entry["iata"]:
                entry["iata"] = b["iata"]["value"]
            if "city" in b:
                city = b["city"]["value"]
                cc = entry["_cityCandidates"].setdefault(
                    city, {"parents": [], "labels": {}}
                )
                if "cityParent" in b:
                    p = b["cityParent"]["value"]
                    if p not in cc["parents"]:
                        cc["parents"].append(p)
                for k in ("labelDe", "labelFr", "labelIt", "labelEn", "labelRm"):
                    if k in b:
                        lang = k[5:].lower()
                        cand = b[k]["value"]
                        cur = cc["labels"].get(lang)
                        if cur is None or len(cand) < len(cur):
                            cc["labels"][lang] = cand
        resolved_with_labels = sum(1 for v in by_uic.values() if v)
        print(
            f"  batch {batch_idx + 1}/{n_batches} ({len(batch)} UICs) → {len(bindings)} bindings, total with labels: {resolved_with_labels}",
            file=sys.stderr,
        )
        # Save progressively so a crash mid-run is recoverable.
        if (batch_idx + 1) % 10 == 0:
            save(out_path, by_uic)
        time.sleep(SLEEP_BETWEEN)

    # Stage 2: collapse city candidates per UIC by picking the deepest
    # P131-nested entity (its parent is also in the station's P131 set),
    # then promote its labels to the UIC's top-level dict.
    for uic, entry in by_uic.items():
        candidates = entry.pop("_cityCandidates", None)
        if not candidates:
            continue
        city_qids = set(candidates.keys())
        deepest = None
        for qid, info in candidates.items():
            if any(p in city_qids for p in info["parents"]):
                # This city has a parent that is itself in the station's
                # P131 set — it is one level deeper.
                deepest = qid
                break
        if deepest is None:
            # No nesting evident — fall back to the candidate with the
            # most language labels. Deterministic via sorted qid tiebreak.
            deepest = max(
                sorted(candidates.keys()),
                key=lambda q: len(candidates[q]["labels"]),
            )
        for lang, label in candidates[deepest]["labels"].items():
            entry[lang] = label

    save(out_path, by_uic)
    with_labels = sum(1 for v in by_uic.values() if any(k in v for k in ("de", "fr", "it", "en", "rm")))
    print(f"wrote {len(by_uic)} entries ({with_labels} with city labels) to {out_path}")


if __name__ == "__main__":
    if len(sys.argv) != 3:
        print("usage: fetch_wikidata_aliases.py <stations.json> <output.json>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1], sys.argv[2])
