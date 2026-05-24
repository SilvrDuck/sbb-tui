package ui

import (
	"strings"
	"unicode"

	"github.com/necrom4/sbb-tui/ui/querycache"
	"github.com/necrom4/sbb-tui/ui/stations"
)

// apiEndorsementBoost is the score added when the SBB locations API has
// endorsed a station for the user's query. It is large enough to lift an
// otherwise unmatched canonical (e.g. Genève for "genf") above any local
// subsequence noise, but the per-row API position is subtracted from it so
// the SBB resolver's own ordering is preserved among co-endorsed hits.
const apiEndorsementBoost = 600

// buildPopoverMatches assembles the popover candidates by combining local
// fuzzy matches with API-resolved hits cached on disk. The local match list
// is the primary source; API hits either boost a co-found UIC or insert as
// a foreign candidate when the local matcher could not reach it.
//
// If neither local fzf nor the cache surface anything strong, a
// Damerau-Levenshtein typo-tolerance pass fires as a fallback so 1- or
// 2-edit mistakes still find their station.
func (m *appModel) buildPopoverMatches(query string) []stations.Match {
	if m.fuzzyIdx == nil {
		return nil
	}

	// Slightly oversample locally so API additions can still mix and we
	// don't drop a strong cached UIC because a borderline local row pushed
	// it past popoverRows.
	const localOversample = 2
	local := m.fuzzyIdx.Search(query, popoverRows*localOversample, m.scoreCfg)

	var hits []querycache.Hit
	if m.apiCache != nil {
		if cached, _, ok := m.apiCache.Lookup(query); ok {
			hits = cached
		}
	}

	merged := mergeLocalAndAPI(local, hits, query, m.fuzzyIdx, m.scoreCfg)

	// Typo fallback fires when nothing strong came through — saves the
	// search from being noisy when fzf and API already have the answer.
	if isWeakResult(merged) {
		maxDist := 1
		if len(query) >= 8 {
			maxDist = 2
		}
		typos := m.fuzzyIdx.SearchTypos(query, maxDist)
		merged = mergeTypoMatches(merged, typos, m.scoreCfg)
	}

	return merged
}

// isWeakResult is true when no row in the merged list has a final score
// above the threshold that fzf produces for a confident match. Tuned by
// inspection: a clean prefix match on a 4-letter query lands around 100;
// a confident multi-bonus match is well above 500.
func isWeakResult(matches []stations.Match) bool {
	if len(matches) == 0 {
		return true
	}
	return matches[0].FinalScore < 200
}

// mergeTypoMatches folds Damerau-Levenshtein hits into the existing
// candidate set. Typo matches that hit an already-found UIC just bump it;
// new UICs are added with a distance-scaled score so 1-edit matches sort
// above 2-edit ones.
func mergeTypoMatches(existing []stations.Match, typos []stations.TypoMatch, cfg stations.ScoreConfig) []stations.Match {
	if len(typos) == 0 {
		return existing
	}
	seen := make(map[string]int, len(existing))
	for i, m := range existing {
		seen[m.Station.UIC] = i
	}
	const typoBase = 400 // 1-edit matches start near a confident fzf hit
	const typoPerEdit = 100
	for _, t := range typos {
		boost := typoBase - typoPerEdit*(t.Distance-1) // distance=1 → 400, =2 → 300
		modeBonus := cfg.ModeBonusFor(t.Station.Mode)
		if pos, ok := seen[t.Station.UIC]; ok {
			existing[pos].Bonus += boost
			existing[pos].FinalScore += boost
			continue
		}
		existing = append(existing, stations.Match{
			Station:    t.Station,
			AliasText:  t.AliasText,
			FZFScore:   0,
			Bonus:      boost + modeBonus,
			FinalScore: boost + modeBonus,
		})
		seen[t.Station.UIC] = len(existing) - 1
	}
	sortMatchesByFinal(existing)
	return clampMatches(existing)
}

// mergeLocalAndAPI is the pure merge logic; broken out for testability and
// so the apiHitsMsg handler can rebuild the popover without rerunning fzf.
func mergeLocalAndAPI(local []stations.Match, hits []querycache.Hit, query string, idx *stations.Index, cfg stations.ScoreConfig) []stations.Match {
	if len(hits) == 0 {
		// Nothing to merge — return local as-is (still possibly oversampled).
		return clampMatches(local)
	}

	seen := make(map[string]int, len(local))
	for i, lm := range local {
		seen[lm.Station.UIC] = i
	}

	aliasLabel := titleCase(query)

	var byUIC map[string]stations.Station
	if idx != nil {
		all := idx.Stations()
		byUIC = make(map[string]stations.Station, len(all))
		for i := range all {
			byUIC[all[i].UIC] = all[i]
		}
	}

	for apiPos, h := range hits {
		boost := apiEndorsementBoost - apiPos // preserve API's own ranking among ties
		if pos, ok := seen[h.UIC]; ok {
			local[pos].Bonus += boost
			local[pos].FinalScore += boost
			continue
		}

		var station stations.Station
		if loc, ok := byUIC[h.UIC]; ok {
			station = loc
		} else {
			station = stations.Station{UIC: h.UIC, Name: h.Name, Mode: modeFromIcon(h.Icon)}
		}

		// Only insert NEW UICs (those the local fzf didn't already surface)
		// when the station is a train-class transit mode. Otherwise a
		// query like "genf" floods the popover with bus stops named
		// "Genève, X" that happen to be in the SBB API response.
		if !isTrainClass(station.Mode) {
			continue
		}
		modeBonus := cfg.ModeBonusFor(station.Mode)
		final := boost + modeBonus
		// Compute matched-char positions for the highlight, so an API-only
		// row (or one whose local fzf hit was too weak to land in the top
		// candidates) still shows red where the query is satisfied. We
		// use the fzf algorithm itself (not a greedy left-to-right walk),
		// so the highlight lands on the optimal contiguous match — e.g.
		// the trailing "Arare" in "Plan-les-Ouates, Arare", not the loose
		// scatter of one 'a' inside Plan plus four more chars across.
		positions := stations.PositionsFor(query, station.Name)
		local = append(local, stations.Match{
			Station:      station,
			AliasText:    aliasLabel, // typed alias surfaces — e.g. "Genf" for the German query
			FZFScore:     0,
			Bonus:        final,
			FinalScore:   final,
			MatchedRunes: positions,
		})
		seen[h.UIC] = len(local) - 1
	}

	sortMatchesByFinal(local)
	return clampMatches(local)
}

func sortMatchesByFinal(m []stations.Match) {
	// Insertion sort — popover list is tiny.
	for i := 1; i < len(m); i++ {
		for j := i; j > 0 && m[j].FinalScore > m[j-1].FinalScore; j-- {
			m[j], m[j-1] = m[j-1], m[j]
		}
	}
}

func clampMatches(m []stations.Match) []stations.Match {
	if len(m) > popoverRows {
		return m[:popoverRows]
	}
	return m
}

// isTrainClass returns true for transit modes on rails (proper train, metro,
// tram, rack railway, cable railway) — used to filter API merge insertions
// so a city-name query doesn't drag in unrelated bus stops named after the
// same city.
func isTrainClass(mode string) bool {
	switch mode {
	case "TRAIN", "METRO", "TRAM", "RACK_RAILWAY", "CABLE_RAILWAY":
		return true
	}
	return false
}

// modeFromIcon maps the SBB locations-API icon string to the local Mode
// vocabulary so a station the local index doesn't know about still gets
// a sensible popularity weight.
func modeFromIcon(icon string) string {
	switch strings.ToLower(icon) {
	case "train":
		return "TRAIN"
	case "tram":
		return "TRAM"
	case "bus":
		return "BUS"
	case "ship":
		return "BOAT"
	case "metro", "subway":
		return "METRO"
	case "cablecar", "cable-car":
		return "CABLE_CAR"
	case "cablerailway", "funicular":
		return "CABLE_RAILWAY"
	case "chairlift":
		return "CHAIRLIFT"
	case "rackrailway", "cogwheel":
		return "RACK_RAILWAY"
	default:
		return ""
	}
}

// titleCase returns s with the first rune of each whitespace-separated
// word uppercased — used to render the user's typed alias ("genf" → "Genf")
// when the popover surfaces an API-only resolution.
func titleCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	upper := true
	for _, r := range s {
		if unicode.IsSpace(r) {
			b.WriteRune(r)
			upper = true
			continue
		}
		if upper {
			b.WriteRune(unicode.ToUpper(r))
			upper = false
		} else {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}
