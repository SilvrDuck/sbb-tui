# Fuzzy Station Picker

End-to-end documentation of the fuzzy station-picker spike: how it
matches, how it ranks, how the test suite was built, and how the scoring
constants were tuned. Everything here is reproducible from the scripts
and data files in the repo.

## Contents

| File | What's in it |
|---|---|
| [architecture.md](./architecture.md) | The runtime pipeline: local fzf → cache lookup → API merge → typo fallback → render |
| [data-pipeline.md](./data-pipeline.md) | How `data/stations.json` is built — SBB Service Points + Wikidata enrichment + refresh cadence |
| [scoring.md](./scoring.md) | The `ScoreConfig` knobs and how they combine on top of fzf's raw score |
| [tuning.md](./tuning.md) | HPO methodology (random search + coordinate descent), parameter grid, train/holdout split, before/after numbers |
| [scenarios.md](./scenarios.md) | The 100-scenario evaluation suite — axes, query styles, how to extend it |
| [ux.md](./ux.md) | Popover keymap, refocus-overwrite affordance, z-index overlay, alias display rules |

## Headline result

| Metric (holdout, n=126) | Zero-bonus baseline | Tuned, alias-enriched | SBB API baseline |
|---|---|---|---|
| MRR @ top-10 | 0.331 | **0.831** | 0.645 |
| Top-1 accuracy | 24.1% | **79.4%** | 58.8% |
| Top-3 accuracy | 39.2% | **83.3%** | 70.6% |
| Top-10 accuracy | 53.3% | **93.7%** | 73.1% |

The local matcher now beats the public SBB locations API on every query
style we measure, including cross-language — which used to be the API's
exclusive strength.

## How to reproduce

```bash
# Rebuild the dataset (SBB + Wikidata, ~5 min)
./scripts/build_data.sh

# Run the eval at the current default config
go run ./cmd/fuzzy-eval

# Re-tune the scoring config (~20 min)
go run ./cmd/fuzzy-eval -tune -tune-samples 500
```

The eval reads `data/scenarios.json` (the 100-case suite),
`data/stations.json` (the alias-enriched index, embedded into the binary),
and `data/api-baseline.json` (the cached SBB API responses).
