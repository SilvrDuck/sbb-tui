# Tuning

We treat the scoring constants as a **hyperparameter-optimisation**
problem: pick values that maximise a quality metric on a curated suite
of realistic queries. Honest framing — this is HPO, not ML in the
gradient-trained sense. The structural assumption (additive scoring
over hand-designed features) is ours; the constants are picked by
search.

## Metric — Mean Reciprocal Rank

For each test case `t` with target UIC `u`:

```
RR(t) = 1 / rank_of(u in top-K)   if u in top-K
RR(t) = 0                          otherwise

MRR = mean(RR(t) for t in T)
```

We also track top-1, top-3, top-10 accuracy and a per-style /
per-pattern breakdown. K = 10 (`popoverRows + 2`).

MRR is the natural choice for a ranked-list task where there's exactly
one "right" answer per query — it penalises getting the target outside
top-N exponentially.

## Test suite

`data/scenarios.json` — see [scenarios.md](./scenarios.md). 100 hand-
curated commute scenarios, expanded to **643 test cases** (one per
query per endpoint per scenario). 80/20 train/holdout split, by
scenario (so all queries for a given station stay on the same side of
the split, preventing leakage).

`data/api-baseline.json` — cached SBB API responses for the same query
strings, used as a comparison baseline. Polled gently (200 ms cadence,
~95 s for ~470 unique queries) and cached so re-runs don't hit the
network.

## Optimisation

Two-phase search, implemented in `cmd/fuzzy-eval`:

### Phase 1 — Random search

500 independent samples drawn uniformly from a 7-parameter discrete
grid:

| Parameter | Grid |
|---|---|
| `PrefixBonus` | 0..400 step 50 (9 values) |
| `AbbrExactBonus` | 0..1000 step 100 (11 values) |
| `ConsecutiveBonus` | 0..50 step 5 (11 values) |
| `ModeBonus[TRAIN]` | 0..500 step 50 (11 values) |
| `ModeBonus[METRO]` | 0..300 step 50 (7 values) |
| `ModeBonus[TRAM]` | 0..200 step 50 (5 values) |
| `ModeBonus[BOAT]` | 0..200 step 50 (5 values) |
| `ChairliftPenalty` | 0..200 step 50 (5 values) |

(`BUS` anchored at 0.)

Total grid size: ≈ 7 M cells. With 500 random samples we cover ≈ 0.007%
— enough to find a reasonable starting point, not enough to be sure
we're at the true optimum. That's the role of phase 2.

### Phase 2 — Coordinate descent refinement

Starting from the best random sample, sweep each parameter's grid in
turn (others fixed), keep the value that maximises MRR. Repeat until a
full pass yields no improvement.

In practice converges in 1–3 passes. The refinement typically moves the
score 0.005–0.020 above the random-search best.

## Performance trick — raw-match cache

A naive eval is `O(samples × tests × aliases)` — for 500 × 643 × 96 902
that's 31 G fzf calls, ~25 minutes wall-clock.

We factor `Index.Search` into `SearchRaw` (the expensive fzf pass) +
`Rank` (cheap bonus add + sort). Then the eval pre-computes
`SearchRaw` once per *unique query* and feeds those raw matches to
every config under test:

```go
type rawCache struct {
    raws   map[string][]stations.RawMatch
    folded map[string]string
}
```

Cache build: ~3 s. Each subsequent `evaluateCached(cfg)` call: ~50 ms.
500 random + 100 descent evals: ~30 s total.

## Train/holdout split

The split is by **scenario ID**, not by test case — so all queries
generated for "Geneva → Lausanne (commute)" stay together. Otherwise
the tuner could memorise that the answer to `genf` is UIC 8501008 from
the train side, then trivially score perfect on the same query in
holdout.

Seed = 42, reproducible.

## Results

### Headline (holdout, n=126)

| Round | What changed | MRR | Top-1 | Top-3 | Top-10 |
|---|---|---|---|---|---|
| 0 | Bug — fzf `Init` not called | 0.045 | 0% | 12.5% | 12.5% |
| 1 | Init fixed, default config = zero bonuses | 0.331 | 24.1% | 39.2% | 53.3% |
| 2 | First random-search tune (no ConsecutiveBonus) | 0.739 | 70.6% | 73.8% | 83.3% |
| 3 | Re-tune with ConsecutiveBonus added | 0.732 | 69.8% | 73.0% | 82.5% |
| 4 | **Wikidata aliases + re-tune** | **0.831** | **79.4%** | **83.3%** | **93.7%** |
| — | SBB API baseline (comparison) | 0.645 | 58.8% | 70.6% | 73.1% |

Round 3 was noisier than round 2 within the same number of samples — the
search space gained one parameter without a proportional sample
increase. Round 4 absorbed both the new parameter and the much larger
alias surface and produced the cleanest numbers.

### Final tuned config (round 4)

```go
ScoreConfig{
    PrefixBonus:      100,
    AbbrExactBonus:   200,
    ConsecutiveBonus: 20,
    ModeBonus: map[string]int{
        "TRAIN": 250,
        "METRO": 150,
        "TRAM":  200,
        "BOAT":  100,
        "BUS":   0,
    },
    ChairliftPenalty: 200,
}
```

Notice the absolute values dropped vs round 2 — `TRAIN` went 450 → 250,
`AbbrExactBonus` 500 → 200. With the alias enrichment, many more
candidates land in the top-N for cross-language queries, so the tuner
relies less on amplifying TRAIN per row and more on pushing junk modes
(chairlift penalty 50 → 200) down.

### Breakdown by query style (round 4, holdout)

```
abbreviation     n=38   MRR=1.000   top-1=100.0%   top-3=100.0%   top-10=100.0%
prefix           n=44   MRR=0.796   top-1= 72.7%   top-3= 79.5%   top-10= 97.7%
substring        n=13   MRR=0.782   top-1= 69.2%   top-3= 84.6%   top-10= 92.3%
typo             n=14   MRR=0.726   top-1= 71.4%   top-3= 71.4%   top-10= 78.6%
cross-language   n=17   MRR=0.667   top-1= 64.7%   top-3= 64.7%   top-10= 82.4%
```

Cross-language was the biggest win: in round 2 (no aliases) it was
**MRR 0.013, top-1 0%** — essentially unsolvable locally. With Wikidata
aliases it jumps 50× to MRR 0.667.

### Breakdown by commute pattern

```
Village-Village    n=12   MRR=1.000   top-1=100.0%
Hub-Spoke          n=26   MRR=0.940   top-1= 92.3%
Touristic          n=24   MRR=0.923   top-1= 91.7%
Hub-Hub            n=12   MRR=0.833   top-1= 75.0%
Airport            n=18   MRR=0.781   top-1= 72.2%
Village-Hub        n=27   MRR=0.682   top-1= 63.0%
International      n= 7   MRR=0.520   top-1= 42.9%
```

International is the remaining gap — foreign UICs (Milano, Mulhouse,
Frankfurt, …) have less Wikidata-city coverage than Swiss ones, and the
SBB API's own coverage of cross-language for foreign stations is
inconsistent.

## Reproducing

```bash
# Default-config baseline
go run ./cmd/fuzzy-eval

# Full tune (~30 s with the raw-match cache, 80/20 split, seed=42)
go run ./cmd/fuzzy-eval -tune -tune-samples 500

# Different sample count / seed
go run ./cmd/fuzzy-eval -tune -tune-samples 1000 -seed 7
```

The output prints the random-search trace, the coordinate-descent
trace, the final config, and the per-style + per-pattern breakdown on
both train and holdout sets.
