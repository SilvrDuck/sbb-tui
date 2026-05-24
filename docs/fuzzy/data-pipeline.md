# Data Pipeline

Single command rebuilds the entire dataset:

```bash
./scripts/build_data.sh
```

That runs four stages in sequence, each idempotent.

## Stage 1 — Download SBB Service Points

Source: [data.sbb.ch — Dienststellen gemäß opentransportdataswiss](https://data.sbb.ch/explore/dataset/dienststellen-gemass-opentransportdataswiss/).

```
https://data.sbb.ch/api/explore/v2.1/catalog/datasets/
  dienststellen-gemass-opentransportdataswiss/exports/csv
  ?lang=en&delimiter=%3B&use_labels=true
```

- 24 MB CSV, ~57 600 rows, refreshed daily at 06:00 Europe/Zurich.
- 47 columns. Notable: `number` (UIC), `designationOfficial` (canonical
  name, ONE language only), `abbreviation` (SBB railway code), `stopPoint`,
  `meansOfTransport`, `validTo`.
- No ETag/Last-Modified — for incremental refresh we have to either
  compare content hashes or query the dataset metadata endpoint for the
  `data_processed` timestamp.

The endpoint has a 6 000 req/day rate limit (more than enough).

## Stage 2 — Distill

`scripts/distill_stations.py` filters and reshapes:

- `stopPoint == "true"` (actual passenger stop, not infrastructure)
- `validTo == "9999-12-31"` (currently active)
- `meansOfTransport ∈ {TRAIN, METRO, TRAM, BUS, BOAT, CABLE_CAR,
  CABLE_RAILWAY, CHAIRLIFT, RACK_RAILWAY, ELEVATOR}` (drops empty +
  UNKNOWN — those are typically POIs and travel-agent registrations
  living in the same dataset)

Output schema:

```json
{
  "uic": "8501008",
  "name": "Genève",
  "abbr": "GE",            // optional
  "mode": "TRAIN"
}
```

Mode distribution after filtering: BUS 44 646, TRAIN 1 729, CHAIRLIFT
595, CABLE_CAR 535, BOAT 382, TRAM 286, CABLE_RAILWAY 127, METRO 80,
RACK_RAILWAY 67, ELEVATOR 4 — total 48 451.

## Stage 3 — Fetch Wikidata enrichment

`scripts/fetch_wikidata_aliases.py` calls the Wikidata SPARQL endpoint
with the UIC list from `stations.json`. For each station, we ask for:

| What | Wikidata property | Used for |
|---|---|---|
| City multilingual labels | `P131` → `?city → rdfs:label@{de,fr,it,en,rm}` | Cross-language city aliases |
| Station alt names | `skos:altLabel` on the station entity | Local nicknames (Cornavin, Eaux-Vives, Kloten, …) |
| IATA airport code | `P276` → `?airport → P238` | Not yet working — needs different traversal |

The link to local data is **purely structural via UIC**: `wdt:P722
"<uic>"` finds the Wikidata station entity, no string matching of names
anywhere.

Implementation notes:

- **POST, not GET** — at 500 UICs per batch the GET URL exceeds 8 KB and
  triggers HTTP 431.
- **Batches of 250** with `VALUES { ... }` — small enough to avoid the
  60 s endpoint timeout.
- **Retry with exponential backoff** for transient 502s.
- **Resume from disk** — `out.json` is reloaded at startup, only missing
  UICs are queried. So if a network blip kills the run mid-way, restart
  picks up where it left off.
- **Filter to UICs of country prefix 7 or 8** — SBB also assigns
  7-digit internal IDs starting with 1-6 to bus stops, those are never
  in Wikidata as UIC-coded entities, so querying them just wastes
  round-trips.
- **User-Agent set** per Wikimedia's [API etiquette](https://meta.wikimedia.org/wiki/User-Agent_policy).

Coverage in the last run: 27 102 UICs queried, **1 816 with city
labels, 1 666 with altLabels** — basically every notable rail station in
Switzerland.

Output: `data/wikidata-aliases.json`, keyed by UIC:

```json
{
  "8501008": {
    "de": "Genf",
    "fr": "Genève",
    "it": "Ginevra",
    "en": "Geneva",
    "rm": "Genevra",
    "alts": ["Cornavin", "Bahnhof Cornavin", "gare de Cornavin", ...],
    "iata": ""
  }
}
```

## Stage 4 — Merge

`scripts/merge_wikidata_aliases.py` derives aliases from the Wikidata
data and bakes them into `stations.json`.

Three derivation rules, all deterministic:

### Rule 1 — City prefix substitution

For each station whose canonical starts with one of its Wikidata city
labels (optionally followed by ` `, `,` or `-`), strip that label and
substitute every other-language label:

```
canonical: "Genève-Aéroport"
city labels: {de: "Genf", fr: "Genève", it: "Ginevra", en: "Geneva"}
matched prefix: "Genève" (fr)
suffix: "-Aéroport"
→ aliases: ["Genf-Aéroport", "Ginevra-Aéroport", "Geneva-Aéroport"]
```

For canonical names that are shorter than the city labels (`Brig` with
city `Brig-Glis`), Rule 1 falls back to a **shared-suffix** pass: find
the longest common trailing string across all labels, strip it, emit the
bare forms.

### Rule 2 — Strip wrappers from station altLabels

Wikidata altLabels include both bare nicknames and wrapped forms:

```
"Cornavin"                     ← keep
"Bahnhof Cornavin"             ← strip prefix → "Cornavin"
"gare de Cornavin"             ← strip prefix → "Cornavin"
"Geneva railway station"       ← strip suffix → "Geneva"
"Hauptbahnhof Genf"            ← strip prefix → "Genf"
"Gare de l'aéroport de Zurich" ← strip "Gare de l'" → "aéroport de Zurich"
```

Wrapper patterns are universal transit-vocabulary regexes, not
station-specific.

### Rule 3 — Transit-class word substitution

A small universal dictionary swaps a single transit-class word in the
canonical for every other-language equivalent:

```
{Flughafen, Aéroport, Aeroporto, Airport, Aeropuerto}
{Bahnhof, Gare, Stazione, Station}
{Hauptbahnhof, Gare centrale, Stazione centrale, Hbf, HB, Centrale, Main}
{SBB, CFF, FFS}
```

```
canonical: "Zürich Flughafen"
→ aliases: ["Zürich Aéroport", "Zürich Aeroporto",
            "Zürich Airport", "Zürich Aeropuerto"]
```

### Filter — TRAIN mode only

Only stations with `mode == "TRAIN"` get aliases. Bus stops, trams,
boats, and chairlifts very rarely benefit from cross-language matching
and would just flood the popover with near-duplicates.

### Filter — municipality-class entities (SPARQL ontology constraint)

Wikidata's `P131` chain occasionally returns multiple cities for one
station — typically the municipality *and* the canton it sits in. When
the municipality entity lacks a label in some language but the canton
does, picking "shortest label per language" mixes them: e.g.
Chêne-Bourg's municipality has no German `rdfs:label`, so DE leaks to
the canton's "Kanton Genf".

We filter at SPARQL time using Wikidata's own ontology — only join
with entities that are instances of "human settlement" (Q486972) or
any subclass via `wdt:P31/wdt:P279*`:

```sparql
?station wdt:P131 ?city .
?city wdt:P31/wdt:P279* wd:Q486972 .
```

Cantons (Q23058), districts (Q748149), regions, etc. all sit outside
the human-settlement subtree, so they never enter the candidate set.
Languages with no municipality-level label just don't contribute
aliases — silent degrade, no wrong data. No hand-curated string
filter; the Wikidata ontology decides what counts as a city.

### Final shape per enriched station

```json
{
  "uic": "8501008",
  "name": "Genève",
  "abbr": "GE",
  "mode": "TRAIN",
  "aliases": ["Cornavin", "Geneva", "Geneve-Cornavin", "Genevra",
              "Genf", "Genf-Cornavin", "Genève-Cornavin", "Ginevra"]
}
```

After Stage 4: **791 stations enriched, 1 249 aliases total**.

## Stage 5 — Embed

```bash
cp data/stations.json ui/stations/stations.json
```

The Go binary `//go:embed`s the file. Single-file deployment, zero
runtime data dependency.

## Refresh cadence

Future first-run download flow (not yet implemented) checks the
SBB dataset metadata endpoint for `data_processed` and a freshness
shorter than 24 h, otherwise re-runs Stages 1–5.

Wikidata is fetched on the same cadence — small enough to bundle.
