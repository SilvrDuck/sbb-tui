# Scenario Suite

`data/scenarios.json` — 100 hand-curated commute scenarios that drive
the eval. Each scenario has a From and a To endpoint with 3–5 query
variants per endpoint, yielding **643 test cases total**.

## Shape

```json
{
  "id": 1,
  "persona": "Geneva resident commuting to a Lausanne tech job",
  "region": "Romandie",
  "pattern": "Hub-Hub",
  "from": {
    "uic": "8501008",
    "name": "Genève",
    "queries": [
      {"text": "gen",     "style": "prefix"},
      {"text": "geneva",  "style": "cross-language", "language": "EN"},
      {"text": "genf",    "style": "cross-language", "language": "DE"},
      {"text": "ginevra", "style": "cross-language", "language": "IT"},
      {"text": "GE",      "style": "abbreviation"}
    ]
  },
  "to": { ... }
}
```

The `uic` field is the ground truth — the eval scores a query as
correct if it produces this UIC in the top-K.

## Design axes

| Axis | Values |
|---|---|
| **Region** | Romandie, Suisse alémanique, Mittelland, Ticino, Graubünden/Valais, International, Aéroport |
| **Commute pattern** | Hub-Hub, Hub-Spoke, Village-Hub, Village-Village, Touristic, Airport, International |
| **Prominence of target** | Major hub (≈30 most-trafficked), Regional center, Village, Obscure rural |
| **Query style** | Canonical prefix, Cross-language alias, Abbreviation (SBB railway code), Typo (1 edit), Mid-string substring |
| **Language of typer** | DE, FR, IT, EN |
| **Mode** | ~87% both-TRAIN; rest include a TRAM/METRO/BUS at one end (Sonogno PostBus, Lausanne tram, Bern airport bus, …) |

### Region distribution

```
Romandie               20
Suisse alémanique      26
Mittelland             14
Graubünden/Valais      13
Touristic              11
Ticino                  9
International          10
Aéroport stops          8
```

### Pattern distribution

```
Hub-Hub            20
Hub-Spoke          20
Village-Hub        23
Village-Village    13
Touristic          11
Airport             8
International       5
```

## Query styles

Each station's `queries` array is hand-picked per persona realism. The
rules used at curation time:

- **`prefix`** — always included; 3–5 lowercased chars of the canonical
  name. The base case.
- **`cross-language`** — only for stations that genuinely have
  multilingual names (Genève, Zürich HB, Bern, Basel SBB, Lausanne,
  Lugano, Sion, Biel/Bienne, Fribourg, Neuchâtel, Chur, plus
  international hubs like Milano, München, Mulhouse, Frankfurt).
  Tagged with the source language so style+language breakdowns work.
- **`abbreviation`** — only when the station has a non-empty `abbr`
  field in `stations.json`. Tests the SBB railway-code lane.
- **`typo`** — included for ~30 % of stations. One or two character
  edits that a real user would plausibly make (`lausnne`, `vieges`,
  `gnf`, `aaron`).
- **`substring`** — for compound or hyphenated names where the user
  often types only the distinctive part: `bachet` for
  `Lancy-Bachet`, `aéroport` for `Genève-Aéroport`, `eaux-vives` for
  `Genève-Eaux-Vives`.

### Resulting query-style counts

```
prefix         221
abbreviation   192
typo           106
cross-language  82
substring       78
```

## Personas

Personas drive the realism of query selection, not the eval directly.
Examples:

- A Romandie student going to ETH
- An Italian-speaking nurse in Mendrisio commuting to Lugano
- A Geneva banker flying to Frankfurt
- A Bernese hiker going to Zermatt
- A Lavaux winemaker going to Vevey-Funi
- A Verzasca-valley resident catching the Sonogno PostBus
- A cross-border worker on the Annemasse → Genève line
- A Bernina-Express retiree crossing into Tirano

The personas are stored in the `persona` field but the eval only uses
`region`, `pattern`, and the `queries[].style` for its breakdown.

## Validation

Every `uic` was looked up in `data/stations.json` before commit — 0
broken references across 200 endpoints (`from` + `to`).

## Extending

To add a scenario, append a new object with a unique `id`. The eval
reads the file at startup; no other registration is needed.

To re-run the eval after editing:

```bash
go run ./cmd/fuzzy-eval                  # baseline
go run ./cmd/fuzzy-eval -tune            # re-tune
```

If you add ≫100 scenarios it may be worth bumping `-tune-samples` to
1 000 — the expanded test set will give the tuner more signal but also
more variance per sample.

## Limitations

- The 100 scenarios skew Swiss — long-tail tiny stations are
  under-represented. International commutes (Milano, Frankfurt) are
  thin enough (n=7 in holdout) that the per-pattern numbers are noisy.
- "Typo" is restricted to 1–2 edits. Multi-edit typos and phonetic
  errors are not tested.
- "Cross-language" assumes the target station actually has a
  meaningful alternate-language name. Most micro-stops don't and
  aren't covered.
