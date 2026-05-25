# `spike/spec-research` — branch report

A handover note for the maintainer covering everything this branch adds.
The branch is a **spike**: it will not be merged as-is, but the
architectural decisions, data pipeline, and tuning results below are
intended to inform the eventual production implementation.

## TL;DR

Adds an fzf-style fuzzy station picker to the From/To inputs, backed by
an embedded distilled SBB Service Points dataset enriched with
multilingual aliases from Wikidata. Picker handles cross-language
queries (`genf` → Genève), abbreviations (`zh hb`), airport codes
(`GVA`, `ZRH`), and **multi-token AND** queries (`mairie brx` →
Bernex, Mairie). Production scoring tuned via random search +
coordinate descent against a 100-scenario suite, then refined with
human A/B labels.

```
holdout MRR  0.83  (vs SBB API baseline 0.65)
top-1        81.3%
top-3        89.7%
```

## Files added / modified

| Area | Files | Purpose |
|---|---|---|
| Matcher engine | `ui/stations/*.go` | Index, fzf v2 wrapper, scoring |
| UI integration | `ui/model.go`, `update.go`, `view.go` | Popover render, refocus-overwrite, key handling |
| Distillation | `scripts/distill_stations.py` | SBB CSV → slim JSON |
| Enrichment | `scripts/fetch_wikidata_aliases.py` | Wikidata SPARQL fetch (per-station) |
| Vocabulary | `scripts/fetch_wikidata_vocab.py` | Wikidata SPARQL fetch (transit concepts) |
| Merge | `scripts/merge_wikidata_aliases.py` | Apply enrichment rules, write final aliases |
| Pipeline | `scripts/build_data.sh` | Run all 4 stages |
| Eval / tuning | `cmd/fuzzy-eval/`, `cmd/sweep-consec/` | MRR measurement + HPO |
| Manual labeling | `cmd/label-gen/`, `scripts/label.html`, `scripts/label_server.py` | A/B preference UI feeding into scoring |
| Demos | `cmd/fuzzy-demo/`, `cmd/smoke-view/` | Interactive REPL + popover render test |
| Data artefacts | `data/*.json` | Stations, scenarios, enrichment intermediates, API cache, label decisions |

Existing files modified:

- `ui/model.go`, `update.go`, `view.go` — wire the picker into the
  existing Bubbletea model
- `api/client.go` — share folding logic with the matcher
- `config/config.go` — `Fuzzy` and `NerdFont` settings
- `main.go` — flag plumbing + lifecycle for the cache
- `go.mod` / `go.sum` — add `github.com/junegunn/fzf`,
  `github.com/charmbracelet/x/ansi`

## Data sources

Four independent sources, all combined offline into a single
`stations.json` that is `//go:embed`'d into the Go binary.

### 1. SBB Service Points (`data.sbb.ch`)

CSV at <https://data.sbb.ch/explore/dataset/dienststellen-gemass-opentransportdataswiss/>.
24 MB, ~57 600 rows, daily refresh. **The canonical source** for
station name, UIC, abbreviation, mode, and active-status.

Distilled at build time (`scripts/distill_stations.py`):

- keep `stopPoint = "true"` (actual passenger stops, not infrastructure)
- keep `validTo = "9999-12-31"` (currently active)
- keep `meansOfTransport ∈ {TRAIN, METRO, TRAM, BUS, BOAT, CABLE_CAR, CABLE_RAILWAY, CHAIRLIFT, RACK_RAILWAY, ELEVATOR}`

Output: 48 451 rows with `{uic, name, abbr?, mode}`. Mode breakdown:
BUS 44 646 / TRAIN 1 729 / TRAM 286 / METRO 80 / … / ELEVATOR 4.

### 2. Wikidata — per-station enrichment

Linked **purely structurally via UIC** (`wdt:P722`): no name matching
between our index and Wikidata anywhere. Per station we ask Wikidata
for:

| Wikidata signal | Used for |
|---|---|
| `wdt:P131` city + multilingual `rdfs:label` | Cross-language city aliases (`Genève` ↔ `Genf` ↔ `Geneva` ↔ `Ginevra`) |
| `skos:altLabel` on the station entity | Local nicknames (`Cornavin`, `Eaux-Vives`, `Kloten`) |
| `wdt:P31 = Q1335652 (airport rail station)` | Identifies airport rail stations |
| `wdt:P138` (named after) → `wdt:P238` | IATA code (`GVA`, `ZRH`) |

Implementation notes (`scripts/fetch_wikidata_aliases.py`):

- **POST not GET** — 500-UIC batches blow past HTTP 431 on GET URLs
- **Batches of 250** with `VALUES { ... }` — under the 60 s endpoint timeout
- **Retry + exponential backoff** on transient 502s
- **Resume from disk** — re-running picks up where a previous run died
- **Multiple-city disambiguation**: when `P131` returns multiple cities
  (typically municipality + canton), we pick the *deepest* one whose
  parent is also in the station's `P131` set. Replaces an earlier
  brittle `re.sub("^Kanton …", "")` hand-curated filter.

### 4. Wikidata — transit vocabulary (concept-level)

A *separate* fetch (`scripts/fetch_wikidata_vocab.py`) of five concept
entities and their multilingual labels + altLabels:

| Q-ID | Concept | Used as |
|---|---|---|
| Q55488 | railway station | wrapper-strip + substitution |
| Q18543139 | central station | wrapper-strip |
| Q1248784 | airport | wrapper-strip + substitution group |
| Q928830 | metro station | wrapper-strip |
| Q2175765 | tram stop | wrapper-strip |

Output: 5 concepts × 4 langs ≈ 20 labels + 63 altLabels. Used by the
merge step (next section) to strip wrapper words like `Bahnhof`,
`gare de`, `stazione di`, `railway station` from altLabels, and to
substitute language equivalents inside canonicals (`Zürich Flughafen`
→ `Zürich Aéroport`, `Zürich Airport`, …).

## Pipeline — 5 stages, fully reproducible

```
./scripts/build_data.sh
```

1. **Distill** SBB CSV → `data/stations.json` (slim shape)
2. **Augment** with GTFS foreign hubs (Konstanz, Karlsruhe Hbf, …)
3. **Fetch** Wikidata transit vocab → `data/wikidata-vocab.json`
4. **Fetch** Wikidata per-station enrichment → `data/wikidata-aliases.json`
5. **Merge** all of the above → updated `data/stations.json` + copy
   to `ui/stations/stations.json` (for `//go:embed`)

Each stage is idempotent. Wikidata fetches resume from prior output —
adding the GTFS layer in Stage 2 only triggers ~11 new Wikidata
batches for the 2 617 new UICs on the next pipeline run.

### Merge rules (`scripts/merge_wikidata_aliases.py`)

Five distinct alias sources, all deterministic, all run only for
TRAIN-mode stations (other modes flood the popover with duplicates).
Final aliases written under each station's `aliases` field.

#### A. City multilingual substitution

When the canonical starts with a Wikidata city label (followed by
` `, `,`, or `-`), strip that label and substitute every other-lang
label:

```
canonical "Genève-Aéroport"  +  {de: Genf, fr: Genève, it: Ginevra, en: Geneva}
  → aliases: Genf-Aéroport, Ginevra-Aéroport, Geneva-Aéroport
```

Fallback when canonical is *shorter* than the labels (`Brig` vs
`Brig-Glis`): longest-common-suffix strip across labels emits bare
forms (`Briga`, `Brigue`).

#### B. Station altLabel strip

Wikidata altLabels arrive both bare (`Cornavin`) and wrapped
(`Bahnhof Cornavin`, `gare de Cornavin`, `Geneva railway station`).
The wrapper vocabulary is the union of all label/altLabel phrases
from the 5 Wikidata transit concepts + their first-word tokenisations
(so `stazione ferroviaria` contributes the bare strip-word
`stazione`).

Longest-match-first strip, applied at both ends, with a universal
preposition/article list pulled out to handle `gare de Cornavin` →
`Cornavin` and `l'aéroport de Zurich` → `aéroport de Zurich`.

#### C. Transit-class substitution

For canonicals containing a Wikidata-known transit word (currently
just the airport concept Q1248784, since its primary labels are all
single-word: `Flughafen`, `aéroport`, `aeroporto`, `airport`),
substitute each peer:

```
"Zürich Flughafen" → "Zürich aéroport", "Zürich airport", "Zürich aeroporto"
```

Railway-station substitution (`Bahnhof ↔ gare ↔ stazione ↔ station`)
is not generated because Q55488's primary labels are multi-word
phrases (`gare ferroviaire`, `stazione ferroviaria`). The wrapper-
strip already covers the *parse* direction; only the *generate*
direction is lost.

#### D. IATA codes

For stations matching `wdt:P31 wd:Q1335652` (airport rail station)
with a Wikidata link to an airport entity via `wdt:P138` (named
after), the airport's `wdt:P238` IATA code becomes an alias. Two
matches in all of SBB: `GVA` → Genève-Aéroport, `ZRH` → Zürich
Flughafen.

#### E. Description filtering

Wikidata altLabels occasionally include building descriptions
miscategorised under `skos:altLabel` instead of `schema:description`
(`CFF: bâtiment des voyageurs, buffet, dépôt, abri, latrines et
halles des marchandises`, `Bahnhof Alpnachstad (1889) mit Lokremise
…`).

Two-part heuristic, both signals OR'd:

- **Colon** — no real Swiss station name uses `:`. Audited
  exhaustively against 2 231 TRAIN altLabels: catches exactly 3 (all
  Bex), zero false positives.
- **Length cap = 60** (data-derived = `2 × longest_canonical_name`,
  where longest SBB canonical is 30 chars). Catches the long
  description-style entries that don't use colon (Alpnachstad,
  Bäretswil, Neuthal — all 60+ chars).

Five description-y rows in the 30-55 char range slip through
(`Bahnhof Embrach-Rorbas (1907) mit Güterschuppen (1876)`, etc.) —
they share length distribution with real altLabels like `gare de
Biel/Bienne Bözingenfeld/Champs-de-Boujean` (50 chars), so no
tighter length cap is possible without false positives. The truly
clean fix would be to query a "station building parts" Wikidata
concept; deferred because picking the right Q-IDs is itself
curation effort (Q860861 turned out to be "sculpture", etc.).

## Matching engine

`ui/stations/stations.go` wraps `junegunn/fzf`'s v2 algorithm.

### Index shape

Each `Station` row contributes multiple `alias` entries to the
in-memory index:

- the canonical `Name`
- the railway `Abbr` (when present)
- every Wikidata-derived alias

Each alias carries a pre-built rune buffer (no allocation on the
hot path) and a pre-normalised folded form (lowercase + diacritic
strip via fzf's normalisation table). Searching iterates over every
alias; deduplication by UIC happens *after* scoring so the best alias
per station wins.

### fzf v2 invariants we depend on

- `algo.Init("default")` is called in package `init()`. Without it,
  every ASCII char is misclassified as whitespace and case folding
  never fires.
- Query is pre-folded before construction of the rune pattern —
  `gen` matches `Geneva` only because we normalise to `geneva`
  ourselves.
- `FuzzyMatchV2` is called with `forward=true, normalize=true,
  caseSensitive=false`, slab reused across the per-keystroke search.

### Multi-token AND (extended-mode)

Whitespace in the query splits it into tokens. Each token is
independently subseq-matched against the alias text via
`FuzzyMatchV2`; a row passes only when *every* token scores > 0.
Total score = sum across tokens, positions = sorted union.

```
"mairie brx"   →   Bernex, Mairie    (token "mairie" hits [8..13], "brx" hits [0,2,5])
"rue gare"     →   Bretonnières, rue de la Gare
```

Order-independent. Single-token queries fall through to the same code
path with one pattern and produce identical results to the original
implementation.

We deliberately avoided fzf's full `BuildPattern` API because it
needs `ChunkCache`/`Item`/`Chunk` infrastructure (designed for
chunked file streams) — overkill for our ~50 k alias list. The
~25-line manual AND implementation has the same semantics as fzf's
extended mode for the AND case; the `!^$'` operators are not yet
supported but would be straightforward extensions.

### Cross-language single-alias limitation

Multi-token AND requires every token to match the *same alias text*.
`genf airport` does *not* find Genève-Aéroport because `genf` only
matches the `Genève-Flughafen` alias (which has no `f` in
`-airport`), and `airport` only matches the `Genève-airport` alias
(which has no `f` for `genf`). The fix would be a "cross-alias AND"
step that lets each token match a different alias of the same
station; not implemented (~30 lines + a highlight rule for which
alias text to render).

## Merge layer — was present, then removed

A separate "merge layer" used to sit between the local matcher and
the popover. It stacked three candidate sources:

1. **Local fzf** against the embedded index
2. **SBB locations API endorsement** (cache-backed at
   `$XDG_CACHE_HOME/sbb-tui/api-queries.json`): boost any UIC the
   local matcher and the API both surfaced; insert train-class UICs
   the local matcher missed.
3. **Damerau-Levenshtein typo fallback**: when the merged top score
   was < 200, fold in 1-2 edit distance matches.

### Why it existed

The picker landed *before* Wikidata enrichment. At that point the
local index could not resolve `genf`, `geneva`, `ginevra`,
`cornavin`, `kloten` — none of those strings were in any of the
canonical names we shipped. The SBB API knew about them internally.
Merging filled the gap.

### Why it was removed

Once the Wikidata enrichment landed, the API merge became
belt-and-suspenders:

| Pre-Wikidata role | Where it lives now |
|---|---|
| Cross-language city lookup (`genf` → Genève) | Embedded — Rule A |
| Nicknames (`Cornavin`, `Kloten`) | Embedded — Rule B |
| Cross-language transit words (`Flughafen` ↔ `Airport`) | Embedded — Rule C |
| Airport IATA (`GVA`, `ZRH`) | Embedded — Rule D |
| Typo tolerance | Was Damerau fallback; removed (didn't pay off in practice) |
| Data drift safety net | Removed; rebuild the dataset to fix |

`ui/merge.go`, `ui/querycache/` and `ui/stations/typo.go` are gone.
`refreshPopover` calls `m.fuzzyIdx.Search` directly. `api/client.go`
is back to its original shape — only `FetchLocations` for the
non-fuzzy ghost-completion path. `--clear-cache` flag removed from
the binary. `appModel.Close()` removed.

The picker is now pure local fzf against the enriched embedded
index. Synchronous, no network, no on-disk cache, no async re-render
mid-typing.

### Cost / trade-off accepted

- **Typo tolerance**: gone. `barsel` no longer surfaces Basel.
  Empirically the Damerau fallback didn't help often enough to
  justify keeping; in practice the user just retypes correctly.
- **Data drift**: if SBB adds new stations between rebuilds, they
  won't appear until the dataset is regenerated. With the build
  pipeline at `scripts/build_data.sh` this is one command.

### If the maintainer wants the typo fallback back

It was contained in `ui/stations/typo.go` (Damerau-Levenshtein with
a first-letter pre-filter) and called from `ui/merge.go::mergeTypoMatches`.
Both files are in git history at commit before the cleanup. The
re-introduction is straightforward; the value is questionable.

## UX / picker UI

The picker is a popover anchored to the focused From/To input, layered
over the existing app via ANSI splicing — none of the original
start-screen / results-screen layout was reflowed. Roughly 1 700 lines
of UI code in `ui/{model,update,view,merge}.go` plus the small
animation registry already in the repo.

### Header tab order

The From / To / swap / isArrivalTime / date / time / search row is
modelled as a single ordered list of `focusable` entries
(`headerOrder` in `ui/model.go`). Each entry has a `kind`
(`kindInput` or `kindButton`) and a `name`. Tab / Shift+Tab walk this
list. All keymap behaviour switches on `headerOrder[tabIndex].kind`,
not on which Bubbletea component is currently focused — this is what
lets the `q` key insert literal text in inputs but quit when a button
is selected.

### Keymap

When the popover is open (a From/To input is focused and has ≥ 2
chars):

| Key | Behaviour |
|---|---|
| Typing a character | Edits input, refilters popover, resets selection to row 0 |
| `Backspace` | Edits input |
| `↑` / `↓` | Move selection within popover (wraps) |
| `Tab` / `Shift+Tab` | Commit highlighted row's canonical, advance to next/prev field |
| `Enter` | Commit highlighted row, run search if both From and To valid |
| `Esc` | Close popover (first press); quit (second press) |
| `Ctrl+C` | Always quit |
| `q` | Insert literal `q` (only quits when a button has focus) |
| `→` | Accept autocomplete suggestion ONLY if cursor at end of value; otherwise move cursor right |

The `→` overload is intentional — fzf-style accept-suggestion is
useful at end-of-string, but mid-string it must defer to cursor
movement so users can edit. The code temporarily clears the
`textinput.KeyMap.AcceptSuggestion` binding when the cursor is not at
the end.

### Refocus-overwrite (type-to-rewrite affordance)

When you Tab back into a From/To field that already has content, the
field enters overwrite mode — same UX as a browser URL bar:

- The existing value renders in `--text-muted` (faded)
- The cursor parks at column 0
- The help bar swaps to `↵ keep    → append    abc rewrite    ⇥ next    ⎋ cancel`

Behaviour while in overwrite mode:

- Any printable char → clear the value, then insert that char as first char
- `→` → cancel overwrite, park cursor at end, continue editing
- `Backspace` → cancel overwrite, park at end, then delete one char
- `Enter` → keep the prior value as-is (don't apply the popover's top match)

State is tracked in `appModel.overwriteOnType[]` per input;
`m.setOverwrite(idx, bool)` toggles it.

### Date / time inputs do their own keystroke handling

`updateInputs` for inputs 2 (date) and 3 (time) strips the delimiters
(`.` or `:`) before validating, then runs `validateDateDigits` /
`validateTimeDigits` digit-by-digit. Impossible partial inputs are
rejected — typing `4` at start-of-date fails immediately (no month
starts with `4`), typing `25` as the first two date digits fails
(no day is 25-something). After validation the delimiters are
re-inserted at fixed positions (`DD.MM.YYYY`, `HH:MM`), so arbitrary
cursor movement still keeps the shape intact.

### Station suggestion ghost-completion

The textinput component has built-in autocomplete via
`SetSuggestions`. Two non-obvious bits make it useful for a transit
picker:

1. **Debounce + sequence number** — suggestion fetches are gated by
   `suggestDebounce = 300ms` and a per-input `suggestSeq` counter.
   Only the latest tick's response is applied; in-flight requests
   from abandoned keystrokes are discarded. Without this, fast typing
   produced visible ghost-flicker as old responses raced.
2. **Prefix-adapted suggestions** — `adaptSuggestions` /
   `prefixMatchLen` graft the user's literal typed prefix onto each
   suggestion before passing it to the textinput. The widget itself
   only does a `HasPrefix` check; without the graft, typing `zur`
   wouldn't ghost-complete to `Zürich HB` because `"Zürich HB"` doesn't
   start with `"zur"` (the `ü` differs). The adaptation also skips
   diacritics and punctuation, so `"st gal"` ghost-completes to
   `St. Gallen` (the `.` and space delta is bridged).

### Popover positioning and overlay

Anchored to the focused input's left column. Spans as wide as the
content needs (alias + arrow + canonical + mode badge + borders),
clamped to never exceed screen width. The arrow position is computed
to land at exactly the column where the *next* header field begins —
mirroring the input row geometry above it.

The popover **paints over** the underlying view (start-screen logo or
results list) rather than reflowing it. Implementation in
`ui/view.go`:

1. Render the underlying view at full `resultsHeight()`
2. Render the popover into its own block
3. For each popover line, splice it over the corresponding base line
   using `ansi.Cut` — both blocks' ANSI styles are preserved

When the user has searched and connection cards are showing, the
popover compacts to 4 rows so at least one full connection stays
visible beneath it.

### Row layout — single vs two column

Two display modes, chosen per row:

- **Single-column**: when the user's diacritic-folded query is a
  substring of the canonical, the alias would just repeat what the
  canonical shows. Collapse to one line: `▶ Renens VD … TRAIN`.
- **Two-column**: when the alias adds info (cross-language, typo,
  alias text differs from canonical), render `alias → canonical`
  with the arrow at the next-field's column position.

The matched characters in the alias column are styled with
`--border-focused` (accent red); the canonical is bold in `--text`.

### Other UI fixes that landed during the spike

- **Search-icon truncation** — `textinput.View()` emits `promptW +
  Width + 1` columns (trailing cursor cell), regardless of focus.
  Previously the truncate-to-width step only fired when
  `ShowSuggestions=true`, which we set to false on From/To to free
  up `→` for popover use. The two extra columns pushed the `⌕`
  button off-screen. Fix: `ansi.Truncate(view, promptW+Width, "")`
  unconditional in `renderHeaderItem`.
- **Minimum terminal size** unchanged at 80 × 24 — below that the
  view degrades to a single warning, popover suppressed.
- **Theme reuse** — no new theme fields; the popover border,
  highlight, and help bindings draw from existing `Theme.BorderFocused`
  and `Theme.Text` / `Theme.TextMuted`.

## Scoring

`ui/stations/score.go` defines the additive bonus structure layered
on top of fzf's raw score:

```go
type ScoreConfig struct {
    PrefixBonus      int                // alias starts with folded query
    AbbrExactBonus   int                // alias text == query (post-fold)
    ConsecutiveBonus int                // per pair of adjacent matched positions
    ModeBonus        map[string]int     // per-mode constant
    ChairliftPenalty int                // negative bonus for chairlift / cable_car / elevator
}
```

Production values (`ui/model.go::defaultScoreConfig`):

```go
PrefixBonus:      100
AbbrExactBonus:   200
ConsecutiveBonus: 300
ModeBonus:        {TRAIN: 250, METRO: 150, TRAM: 200, BOAT: 100, BUS: 0}
ChairliftPenalty: 200
```

### Tuning — two layers

1. **HPO** against a 100-scenario / 643-test-case suite at
   `data/scenarios.json` (`cmd/fuzzy-eval`):
   - 500-sample random search over the ScoreConfig grid
   - Coordinate descent refinement
   - 80/20 train/holdout split by scenario ID, seed=42
   - Final holdout MRR 0.83 vs SBB API baseline 0.65

2. **Human A/B labels** (`scripts/label.html` + `cmd/label-gen` +
   `scripts/label_server.py`). The labeler labels 100+ generated
   query/AB ranking pairs. Decisions persist per-click to
   `data/label-decisions.jsonl` (fsync'd, survives crash).

   The label round revealed that the HPO-tuned `ConsecutiveBonus=20`
   under-weighted contiguous matches relative to how a human reads
   the popover: typing `renens` should put `Renens VD` (TRAIN
   canonical, full-contiguous match) above `Grenchen Süd` (TRAIN,
   scattered match). Bumped to `ConsecutiveBonus=300`. Re-ran HPO
   sweep — holdout MRR moved 0.839 → 0.863, *improvement* despite
   the heavy weight.

3. **`AbbrExactBonus` relaxation** (post-IATA): originally fired only
   when the alias was a railway abbreviation AND equal to the query.
   Relaxed to fire on *any* exact alias match — IATA codes (`GVA`),
   city labels (`Genf`), single-word nicknames (`Cornavin`) all
   benefit. Breaks ties between similar short queries
   cleanly (e.g. `GVA` vs Grandval's `GVAL` abbreviation).

## Features summary

| Feature | Source | Coverage |
|---|---|---|
| Cross-language city aliases | Wikidata P131 → city rdfs:label | 1 157 TRAIN stations enriched (incl. Konstanz → Constance/Costanza, Mulhouse → Mülhausen, Milano → Mailand/Milan) |
| Station nicknames | Wikidata skos:altLabel + wrapper strip | most major stations have ≥ 1 nickname |
| Airport word substitution | Wikidata Q1248784 labels | Genève-Aéroport, Zürich Flughafen |
| IATA codes | Wikidata Q1335652 + P138 → P238 | GVA, ZRH (exhaustive — only 2 in SBB) |
| Foreign hub coverage | GTFS `stops.txt` parents | 2 617 added (Konstanz, Karlsruhe, Milano Centrale, Paris-Est, …) |
| Multi-token AND | manual fzf extended-mode | all queries with whitespace |
| Description filtering | colon + length-cap | drops ~6 Wikidata description rows |
| Popover overlay | ANSI-aware z-index splicing | start-screen and results-screen alike |
| Refocus-overwrite | type-to-rewrite affordance | mimics browser URL bar UX |

## Constraints on the project

Things downstream maintainers should know about:

1. **`go:embed` of `ui/stations/stations.json`** — the slim dataset
   ships *inside* the binary. ~3 MB after distillation. Means a
   single-file release; means refresh requires a rebuild. A future
   first-run download flow (not yet implemented) would let us drop
   this and pull fresh from `data.sbb.ch` daily.

2. **Wikidata at build time** — refreshing aliases requires hitting
   the public Wikidata SPARQL endpoint with a User-Agent identifying
   the project. Endpoint has rate limits and occasional 502s; the
   fetch script handles both with retry + resume, but expects ~10
   minutes of wall-clock for a full rebuild (27 102 UICs × 250-batch
   = 109 batches, 0.5 s polite delay).

3. **No tests in the repo** (per the original project's stance).
   Validation is via `cmd/fuzzy-eval` (synthetic scenarios),
   `cmd/sweep-consec` (parameter sweep), `cmd/fuzzy-demo` (manual
   REPL), and human A/B labeling. If tests do land later, the
   scoring config and merge rules are the natural surface to
   regress-test.

4. **Mode filter for alias enrichment** — only `mode == TRAIN`
   stations get Wikidata aliases. BUS/TRAM/METRO/etc. rows keep just
   their canonical and abbreviation. Rationale: cross-language city
   substitution applied to BUS stops would 4× the popover with
   near-duplicates that the user almost never wants.

5. **TRAIN-mode bias in popover ranking** — `ModeBonus[TRAIN]=250`
   means a TRAIN station with the same fzf+contig score as a BUS sub-
   stop wins. This is desirable for SBB's typical user (intercity
   travel) but skews against users who want city tram/bus searches.
   Could be made user-configurable.

6. **`revive` lint requires doc comments on every exported
   identifier.** All new exported types and functions in `ui/stations`,
   `ui/querycache`, etc. carry godoc-style comments. Adding an
   exported identifier later without a comment will fail lint.

7. **Pre-commit hooks installed via mise** — `gofumpt`, `goimports`,
   `golangci-lint` on every commit; `conventional-pre-commit` on the
   commit message. Run with `pre-commit run --all-files`.

## Open issues / deferred work

- **Cross-alias AND** — `genf airport` should resolve to Genève-
  Aéroport. Each token currently must match the same alias text.
- **Five short Wikidata descriptions** still slip past the colon +
  length-60 filter (`Bahnhof Embrach-Rorbas (1907) mit
  Güterschuppen (1876)`, etc.). The clean fix is a Wikidata
  "station building parts" concept lookup; picking the right Q-IDs
  is non-trivial (Q860861 turned out to be "sculpture").
- **More airport Q1248784 substitutes** — currently only single-word
  primary labels participate, so railway substitution
  (`Bahnhof ↔ gare ↔ stazione`) is missing in the generate
  direction. Wrapper-strip covers parse direction.
- **First-run download flow** — replace `//go:embed` of the dataset
  with a daily refresh against `data.sbb.ch` (the dataset metadata
  endpoint has a `data_processed` timestamp).
- **fzf extended operators `!`, `^`, `$`, `'`** — not yet supported
  in our manual multi-token AND. ~10 lines each if useful.
- **Test suite** — none. The label round produced
  `data/label-decisions.jsonl` which is a natural seed for a
  regression set.

## Related docs

The longer-form notes live under `docs/fuzzy/`:

- [`architecture.md`](fuzzy/architecture.md) — package boundaries
- [`data-pipeline.md`](fuzzy/data-pipeline.md) — the 4-stage build
- [`scoring.md`](fuzzy/scoring.md) — bonus calculations in detail
- [`tuning.md`](fuzzy/tuning.md) — HPO + label-round notes
- [`scenarios.md`](fuzzy/scenarios.md) — the 100-scenario suite
- [`ux.md`](fuzzy/ux.md) — popover, keymap, overwrite affordance
