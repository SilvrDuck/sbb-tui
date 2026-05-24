# Scoring

A station match's final score is **fzf v2's raw score** plus **additive
bonuses** from a configurable `ScoreConfig`. The additive design means
each lever is independently interpretable and tunable.

```
final = fzf_score(query, alias)
      + PrefixBonus      if alias starts with query (folded)
      + AbbrExactBonus   if alias is the SBB railway code AND equals query
      + ConsecutiveBonus × consecutive_matched_pairs
      + ModeBonus[mode]  per transport mode
      − ChairliftPenalty if mode ∈ {CHAIRLIFT, CABLE_CAR, ELEVATOR}
```

## The fzf score layer

`junegunn/fzf/src/algo.FuzzyMatchV2` returns a Smith-Waterman optimal
subsequence-match score (int16) plus the matched character positions.
The fzf score already rewards:

- Each matched character (+16)
- Consecutive matches (+8 each)
- Word-boundary first character (+10 boundary-white, +8 boundary-word)
- First character of pattern (× 2 multiplier on its bonus)
- Penalises gaps (−3 start, −1 extension)

We pass `caseSensitive=false, normalize=true, forward=true` — so `ü → u`,
`é → e`, `Ç → c` etc. happen automatically inside the matcher.

**Two non-obvious gotchas** that bit us:

1. **`algo.Init("default")` must be called** before any `FuzzyMatchV2`
   call. The package-global `asciiCharClasses` table is initialised
   inside `Init`; without it, every ASCII letter is misclassified as
   whitespace and case folding never fires. We call it from a package
   `init()`.

2. **The pattern must already be lowercase + folded.** The algo
   normalises the *input* on the fly but trusts the *pattern* as-is.
   `Rank` always calls `foldString(query)` before constructing the
   pattern rune slice.

## Additive bonuses

### `PrefixBonus`

Fires when the folded alias begins with the folded query — i.e. the
alias starts with what the user typed.

```
query: "gen"      alias: "Genève"   → +PrefixBonus (Genève[:3] == gen)
query: "gen"      alias: "Argentina" → no bonus (Argentina[:3] != gen)
```

Distinct from fzf's own start-of-word bonus, which only applies to the
first matched character.

### `AbbrExactBonus`

Fires when the alias is a station abbreviation **and** the lowercased
query equals it. So typing `GE`, `ZUE`, `BS`, `BN`, `LS` jumps the
respective hub to the top.

```
query: "GE"   abbr alias: "GE"  → +AbbrExactBonus
query: "GE"   canonical: "Genève"  → no AbbrExactBonus (canonical-kind alias)
```

### `ConsecutiveBonus`

Per consecutive matched-character pair, on top of fzf's internal
`BonusConsecutive`. We're searching station names, not paths — disjoint
subsequence (`g…e…n…f` scattered across an 18-char string) is much less
meaningful here than in directory navigation, so this lever lifts
contiguous matches.

```
query: "bachet"   alias: "Lancy-Bachet"  → 5 consecutive pairs (b-a-c-h-e-t)
                                          → +5 × ConsecutiveBonus
```

### `ModeBonus`

Per-mode bonus. `BUS` is conventionally 0 (anchor); `TRAIN` highest,
others scaled appropriately. Lets the matcher prefer a `TRAIN` station
over a `BUS` stop when both have similar fzf scores.

The current tuned values came out as `TRAIN 250 · METRO 150 · TRAM 200
· BOAT 100 · BUS 0`. Note `TRAM > METRO` — there are more multi-language
tram station aliases in Wikidata than metro ones, so the tuner pushed
TRAM up to compensate. Not a strong signal; either order works
empirically.

### `ChairliftPenalty`

Subtracted from any chairlift, cable-car, or elevator match. These are
seldom what a user means; the penalty pushes them below proper rail
stops even when fzf scores them well.

## Composition with sources beyond local fzf

The popover's candidate set is built in `ui/merge.go`. After local fzf
+ ranking:

- **API hits** (cached or freshly fetched) get an additional
  `apiEndorsementBoost = 600 − api_position` — preserves the SBB
  resolver's own ranking among tied UICs.
- **Typo fallback hits** (Damerau-Levenshtein) get
  `typoBase − typoPerEdit × (distance − 1)` (currently 400 / 100).

These boosts are intentionally **outside** `ScoreConfig` — they're
source-confidence adjustments, not station-quality signals. Combining
them with mode bonuses works naturally: an API-confirmed TRAIN UIC ends
up well above a local-only BUS hit.

## How the tuner sees this

The HPO grid (see [tuning.md](./tuning.md)) covers each `ScoreConfig`
parameter independently. fzf's internal scoring is treated as a fixed
input — we don't try to retune fzf's constants because they are
already well-validated upstream and changing them would break the
algorithm's documented contract.
