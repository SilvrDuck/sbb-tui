#!/usr/bin/env python3
"""Fetch the multilingual labels (and altLabels) of universal transit
concept entities from Wikidata. The merge step then uses these to drive
both the altLabel wrapper-stripping and the transit-class word
substitution — replacing the previously hand-curated WRAPPER_PREFIXES /
WRAPPER_SUFFIXES / TRANSIT_CLASS_GROUPS lists.

Concepts queried:
    Q55488    railway station    → Bahnhof / gare / stazione / station / …
    Q18543139 central station    → Hauptbahnhof / Hbf / gare centrale / …
    Q1248784  airport            → Flughafen / aéroport / aeroporto / airport / …
    Q928830   metro station      → U-Bahnhof / station de métro / subway / …
    Q2175765  tram stop          → Straßenbahnhaltestelle / arrêt de tram / …

Run:
    python3 scripts/fetch_wikidata_vocab.py data/wikidata-vocab.json

Output shape:
    {
      "Q55488": {
        "concept": "railway station",
        "labels": {"de": "Bahnhof", "fr": "gare ferroviaire", ...},
        "alts":   ["Bahn", "gare", "stazione", "station", ...]
      },
      ...
    }

Languages: de, fr, it, en, rm (the languages we already use for
station-level data).
"""

import json
import sys
from urllib.parse import urlencode
from urllib.request import Request, urlopen

ENDPOINT = "https://query.wikidata.org/sparql"
USER_AGENT = "sbb-tui-spike/0.1 (https://github.com/Necrom4/sbb-tui)"
LANGS = ("de", "fr", "it", "en", "rm")

CONCEPTS = {
    "Q55488": "railway station",
    "Q18543139": "central station",
    "Q1248784": "airport",
    "Q928830": "metro station",
    "Q2175765": "tram stop",
}

QUERY = """
SELECT ?concept ?kind ?text WHERE {{
  VALUES ?concept {{ {values} }}
  {{
    ?concept rdfs:label ?text .
    FILTER(LANG(?text) IN ({langs}))
    BIND("label" AS ?kind)
  }} UNION {{
    ?concept skos:altLabel ?text .
    FILTER(LANG(?text) IN ({langs}))
    BIND("alt" AS ?kind)
  }}
}}
"""


def fetch() -> list[dict]:
    values = " ".join(f"wd:{q}" for q in CONCEPTS)
    langs = ",".join(f'"{l}"' for l in LANGS)
    body = urlencode({"query": QUERY.format(values=values, langs=langs)}).encode("utf-8")
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


def main(out_path: str) -> None:
    bindings = fetch()
    out: dict = {}
    for b in bindings:
        qid = b["concept"]["value"].split("/")[-1]
        if qid not in CONCEPTS:
            continue
        entry = out.setdefault(qid, {"concept": CONCEPTS[qid], "labels": {}, "alts": []})
        text = b["text"]["value"]
        if b["kind"]["value"] == "label":
            # SPARQL's LANG() returns lowercase. Prefer the shortest label
            # per language (Wikidata sometimes has plural / phrase forms).
            lang = _detect_lang(text, b)
            if lang:
                cur = entry["labels"].get(lang)
                if cur is None or len(text) < len(cur):
                    entry["labels"][lang] = text
        else:
            if text not in entry["alts"]:
                entry["alts"].append(text)

    # Deterministic output ordering.
    for entry in out.values():
        entry["alts"] = sorted(entry["alts"])

    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(out, f, ensure_ascii=False, indent=2, sort_keys=True)

    total_labels = sum(len(e["labels"]) for e in out.values())
    total_alts = sum(len(e["alts"]) for e in out.values())
    print(f"wrote {len(out)} concepts ({total_labels} labels, {total_alts} altLabels) to {out_path}")


def _detect_lang(_text: str, binding: dict) -> str:  # noqa: ARG001 — kept for call-site clarity
    # rdfs:label includes an "xml:lang" attribute in the JSON results
    # format; Wikidata puts it in the binding's "text" field.
    return binding.get("text", {}).get("xml:lang", "")


if __name__ == "__main__":
    if len(sys.argv) != 2:
        print("usage: fetch_wikidata_vocab.py <output.json>", file=sys.stderr)
        sys.exit(2)
    main(sys.argv[1])
