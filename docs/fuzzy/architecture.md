# Architecture

The fuzzy picker is layered. Every keystroke into a From/To input flows
through the same pipeline:

```
            ┌─────────────────────────────────────────────────────────┐
            │  user keystroke                                         │
            └─────────────────────────────────────────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              │                     │                     │
              ▼                     ▼                     ▼
   ┌──────────────────┐   ┌──────────────────┐   ┌─────────────────┐
   │ Local fzf vs     │   │ On-disk cache    │   │ Debounced SBB   │
   │ in-memory index  │   │ lookup           │   │ API fetch       │
   │ (instant)        │   │ (instant)        │   │ (~300 ms)       │
   └────────┬─────────┘   └─────────┬────────┘   └────────┬────────┘
            │                       │                     │
            └───────────┬───────────┘                     │
                        ▼                                 │
            ┌─────────────────────────────┐               │
            │ mergeLocalAndAPI            │◄──────────────┘
            │ • dedup by UIC              │  (later, replaces popover)
            │ • boost confirmed matches   │
            │ • insert API-only UICs      │
            │   (train-class only)        │
            └────────────┬────────────────┘
                         │
                         ▼
            ┌─────────────────────────────┐
            │ Result weak?                │
            │ (top score < 200)           │
            └──┬─────────────────────┬────┘
            no│                  yes│
              │                     ▼
              │       ┌─────────────────────────────┐
              │       │ Damerau-Levenshtein fallback│
              │       │ • first-letter filter       │
              │       │ • distance ≤ ⌈len/4⌉        │
              │       │ • merge into candidates     │
              │       └─────────────┬───────────────┘
              │                     │
              └─────────┬───────────┘
                        ▼
            ┌─────────────────────────────┐
            │ sortByFinalDesc + clampN    │
            │ → popover state             │
            └─────────────────────────────┘
                        │
                        ▼
                  popover render
        (z-indexed overlay above body)
```

## Components

### `ui/stations` — the matcher

`Index` holds two parallel arrays: stations and aliases. Each alias is a
queryable string with a pre-built `util.Chars` buffer that junegunn's
`FuzzyMatchV2` consumes without allocation on the hot path.

- **`SearchRaw(query) []RawMatch`** — runs `FuzzyMatchV2` against every
  alias; returns the non-zero matches.
- **`Rank(raws, queryFolded, cfg, limit) []Match`** — applies the
  additive bonuses from `ScoreConfig`, deduplicates by UIC keeping the
  best-scoring alias per station, sorts by `FinalScore` descending.
- **`Search(query, limit, cfg)`** — convenience wrapper around the two.
- **`SearchTypos(query, maxDist)`** — Damerau-Levenshtein fallback,
  first-letter pre-filter.
- **`PositionsFor(query, name)`** — runs fzf against a single string,
  used by the API-merge path to produce matched-char highlight indices
  for rows the local index didn't already score.

The matcher requires **`algo.Init("default")`** at package import time
(handled in `init()`). Without it, the global ASCII character-class table
is zero-filled and case folding silently no-ops.

### `ui/querycache` — the persistent cache

In-memory map keyed by normalised (lowercase, NFC, trimmed) query, value
is a list of `{UIC, Name, Icon, FetchedAt}` hits.

- Loads on startup from `$XDG_CACHE_HOME/sbb-tui/api-queries.json`.
- Persists atomically (temp + rename) every 5 inserts plus on graceful
  shutdown via `appModel.Close()`.
- TTL is 30 days; stale entries serve immediately while a background
  refresh re-fetches.
- Empty responses are not cached — they teach the index nothing.

### `ui/merge.go` — the combiner

`buildPopoverMatches(query)` orchestrates everything:

1. `Index.Search(query, popoverRows*2, scoreCfg)` — oversample so API
   merges can mix in.
2. `apiCache.Lookup(query)` — instant cache lookup.
3. `mergeLocalAndAPI(local, hits, query, idx, cfg)` — for each API hit:
   - In local already? Boost (preserve API's own ordering as tie-break).
   - Not in local? Synthesize a `Station` (from local index by UIC, or
     `Station{UIC, Name, Mode: modeFromIcon(icon)}` for foreign).
     **Skip if not train-class** (TRAIN/METRO/TRAM/RACK_RAILWAY/CABLE_RAILWAY).
   - Highlight positions: `stations.PositionsFor(query, name)` so the
     row still lights up the matched chars.
4. `isWeakResult(merged)` — if top score < 200, fire `SearchTypos`
   and merge those in with `typoBoost = 400 − 100·(distance−1)`.
5. Sort, clamp to `popoverRows`, return.

### `ui/update.go` — message loop

- `tea.KeyMsg` for typing → `refreshPopover` → `buildPopoverMatches` →
  schedule `tea.Tick(300ms)` with `suggestSeq[inputIdx]++` to debounce
  the API fetch.
- `suggestTickMsg` (300 ms later) → branch on `m.fuzzy`:
  `fetchAPIHitsCmd` → `apiHitsMsg`.
- `apiHitsMsg` — sequence-guarded; on match, insert hits into cache and
  rebuild popover via `buildPopoverMatches` (now with the fresh cache).

The same `suggestSeq` mechanism that existed in mainline gates the async
response; abandoned keystrokes drop without race.

### `ui/view.go` — render

The popover is rendered as a bordered panel sized to its content, then
ANSI-spliced onto the body via `ansi.Cut`. The body (start screen with
SBB logo, or connection list) renders at full results-height; the
popover replaces only the rows it covers, leaving the logo visible
underneath.

Anchor: popover's left border aligns with the focused input's left
border. Column layout inside the popover mirrors the From/To input row
above — aliases under From's column, arrow at the To-box edge,
canonicals under To's column.

Row collapse: when the typed query is a diacritic-folded substring of
the canonical, the row renders single-column (no arrow). Two-column +
arrow only appears for cross-language or API-resolved-address rows where
the alias actually adds information.

## What was added beyond mainline

| Area | Before | After |
|---|---|---|
| Index | None — every keystroke hit the API | Embedded 48 451 stations + 791 with multilingual aliases |
| API role | Sole source of suggestions | Merged with local fzf; serves cross-language gaps and addresses |
| Caching | None | Persistent disk cache, TTL 30d, learns over time |
| Typo tolerance | None (fzf would just return nothing) | DL fallback when local results are weak |
| Scoring | Plain fzf's internal score | Additive `ScoreConfig` — prefix, abbr-exact, consecutive, mode, chairlift penalty |
| UX | Inline ghost completion | Bordered popover, keyboard-navigable, anchored, z-indexed over the logo, refocus-overwrite affordance |
