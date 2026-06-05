// Package stations loads the distilled SBB Service Points dataset and runs
// fzf-style fuzzy matching against canonical names and railway abbreviations.
//
// The dataset lives in data/stations.json (committed for spike iteration; we
// will swap to embed or first-run download later).
package stations

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

//go:embed stations.json
var rawDataset []byte

// init is required by junegunn/fzf — Init populates the ASCII character-class
// table the matcher consults on every comparison. Without it, every ASCII
// character is treated as whitespace, case folding never fires, and "gen"
// fails to match "Geneva".
func init() {
	algo.Init("default")
}

// Station is one row of the distilled dataset.
type Station struct {
	UIC     string   `json:"uic"`
	Name    string   `json:"name"`
	Abbr    string   `json:"abbr,omitempty"`
	Mode    string   `json:"mode"`
	Aliases []string `json:"aliases,omitempty"` // multilingual aliases (from Wikidata cities)
}

// aliasKind distinguishes canonical-name aliases from abbreviation aliases,
// which receive different scoring treatment.
type aliasKind int

const (
	aliasCanonical aliasKind = iota
	aliasAbbr
)

// alias is a queryable string pointing at a Station, with the rune-buffer
// fzf needs pre-built so we never reallocate on the hot path. We also keep
// a pre-normalised folded form for the cheap prefix-bonus check.
type alias struct {
	text    string
	folded  string // lowercased + diacritics stripped, used for prefix detection
	chars   util.Chars
	station *Station
	kind    aliasKind
}

// Index is an in-memory store of stations and their queryable aliases.
type Index struct {
	stations []Station
	aliases  []alias
}

// Load parses the embedded dataset and returns a ready-to-query Index.
func Load() (*Index, error) {
	var stations []Station
	if err := json.Unmarshal(rawDataset, &stations); err != nil {
		return nil, fmt.Errorf("loading stations: %w", err)
	}

	idx := &Index{stations: stations}

	// Aliases per station: canonical name, the railway abbreviation when
	// present, and each Wikidata-derived multilingual variant
	// ("Genf-Aéroport" for the Genève-Aéroport canonical, "Viège" for
	// Visp, etc.). Each becomes an independently queryable entry so the
	// fzf matcher can land on the language form the user typed.
	idx.aliases = make([]alias, 0, len(stations)*3)
	for i := range idx.stations {
		s := &idx.stations[i]
		idx.aliases = append(idx.aliases, alias{
			text:    s.Name,
			folded:  foldString(s.Name),
			chars:   util.ToChars([]byte(s.Name)),
			station: s,
			kind:    aliasCanonical,
		})
		if s.Abbr != "" {
			idx.aliases = append(idx.aliases, alias{
				text:    s.Abbr,
				folded:  strings.ToLower(s.Abbr),
				chars:   util.ToChars([]byte(s.Abbr)),
				station: s,
				kind:    aliasAbbr,
			})
		}
		for _, aText := range s.Aliases {
			idx.aliases = append(idx.aliases, alias{
				text:    aText,
				folded:  foldString(aText),
				chars:   util.ToChars([]byte(aText)),
				station: s,
				kind:    aliasCanonical,
			})
		}
	}

	return idx, nil
}

// Stations returns the underlying slice for direct UIC lookup.
func (i *Index) Stations() []Station { return i.stations }

// foldString lowercases and strips diacritics using fzf's normalisation
// table — kept consistent with the matcher itself so prefix detection
// agrees with what FuzzyMatchV2 sees.
func foldString(s string) string {
	runes := []rune(strings.ToLower(s))
	for i, r := range runes {
		runes[i] = algo.NormalizeRunes([]rune{r})[0]
	}
	return string(runes)
}

// RawMatch is one alias that fzf scored > 0 against a query, before any
// ScoreConfig bonus is applied. Stored once per alias so the tuner can
// re-rank under different configs without recomputing fzf.
type RawMatch struct {
	UIC         string
	Station     Station
	AliasText   string
	AliasIsAbbr bool
	AliasFolded string
	Mode        string
	FZFScore    int
	Positions   []int
}

// match is a scored hit produced by the matcher; exported as Match below.
type match struct {
	alias     *alias
	fzfScore  int
	bonus     int
	positions []int
}

// Match is a public scored hit returned by Search.
type Match struct {
	Station      Station
	AliasText    string // the alias string that produced the match
	AliasIsAbbr  bool   // true when AliasText is the abbreviation
	FZFScore     int    // raw junegunn score
	Bonus        int    // total bonus added by ScoreConfig
	FinalScore   int    // FZFScore + Bonus — sort key
	MatchedRunes []int  // rune indexes into AliasText for highlighting
}

// SearchRaw scores every alias against the query with fzf v2 and returns
// the non-zero matches in arbitrary order. ScoreConfig bonuses are NOT
// applied; call Rank to combine these with a config into a ranked Match list.
// Split out so the tuner can compute fzf once per query and try many configs
// cheaply against the cased results.
//
// Whitespace in the query is treated as fzf's extended-mode AND: tokens are
// split on runs of spaces and each token must independently match (subseq)
// the alias text. So `mairie brx` matches `Bernex, Mairie` (token "mairie"
// hits the canonical's tail, token "brx" hits the head). Token order does
// not matter. Single-token queries fall through to the same code path with
// one token and produce identical results to the pre-extended-mode matcher.
func (i *Index) SearchRaw(query string) []RawMatch {
	if query == "" {
		return nil
	}

	queryFolded := foldString(query)
	tokens := strings.Fields(queryFolded)
	if len(tokens) == 0 {
		return nil
	}
	patterns := make([][]rune, len(tokens))
	for k, t := range tokens {
		patterns[k] = []rune(t)
	}
	slab := util.MakeSlab(slabSize16, slabSize32)

	out := make([]RawMatch, 0, 64)
	for j := range i.aliases {
		a := &i.aliases[j]
		totalScore := 0
		var allPos []int
		matchedAll := true
		for _, pattern := range patterns {
			res, pos := algo.FuzzyMatchV2(
				false, true, true,
				&a.chars,
				pattern,
				true,
				slab,
			)
			if res.Score == 0 {
				matchedAll = false
				break
			}
			totalScore += res.Score
			if pos != nil {
				allPos = append(allPos, *pos...)
			}
		}
		if !matchedAll {
			continue
		}
		out = append(out, RawMatch{
			UIC:         a.station.UIC,
			Station:     *a.station,
			AliasText:   a.text,
			AliasIsAbbr: a.kind == aliasAbbr,
			AliasFolded: a.folded,
			Mode:        a.station.Mode,
			FZFScore:    totalScore,
			Positions:   allPos,
		})
	}
	return out
}

// Rank applies cfg's bonuses to raw matches, deduplicates by UIC keeping the
// highest-final-score alias per station, sorts by final score descending,
// and returns up to limit Matches.
//
// queryFolded must be the lowercased + diacritic-folded form of the original
// query (matching what SearchRaw matched against).
func Rank(raws []RawMatch, queryFolded string, cfg ScoreConfig, limit int) []Match {
	if len(raws) == 0 || limit <= 0 {
		return nil
	}

	type scored struct {
		raw   *RawMatch
		bonus int
		final int
	}
	best := make(map[string]scored, len(raws))

	for k := range raws {
		r := &raws[k]
		bonus := cfg.modeBonus(r.Mode)

		if len(r.AliasFolded) >= len(queryFolded) && r.AliasFolded[:len(queryFolded)] == queryFolded {
			bonus += cfg.PrefixBonus
		}
		// Exact alias match (alias text == query post-fold). Originally
		// only fired for railway abbreviations, but the same logic
		// applies to any short alias whose entire content the user
		// typed verbatim — IATA codes ("GVA" → Genève-Aéroport), bare
		// city labels ("Genf"), single-word nicknames ("Cornavin").
		// Breaks ties cleanly when two stations share an fzf score on
		// a short query.
		if r.AliasFolded == queryFolded {
			bonus += cfg.AbbrExactBonus
		}
		if cfg.ConsecutiveBonus != 0 {
			bonus += cfg.ConsecutiveBonus * consecutivePairs(r.Positions)
		}

		final := r.FZFScore + bonus
		cur, exists := best[r.UIC]
		if !exists || final > cur.final {
			best[r.UIC] = scored{raw: r, bonus: bonus, final: final}
		}
	}

	out := make([]Match, 0, len(best))
	for _, s := range best {
		out = append(out, Match{
			Station:      s.raw.Station,
			AliasText:    s.raw.AliasText,
			AliasIsAbbr:  s.raw.AliasIsAbbr,
			FZFScore:     s.raw.FZFScore,
			Bonus:        s.bonus,
			FinalScore:   s.final,
			MatchedRunes: s.raw.Positions,
		})
	}

	sortByFinalDesc(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Search is the simple end-to-end convenience that composes SearchRaw + Rank.
// Use this from the TUI; use SearchRaw + Rank directly from the tuner.
func (i *Index) Search(query string, limit int, cfg ScoreConfig) []Match {
	if query == "" || limit <= 0 {
		return nil
	}
	raws := i.SearchRaw(query)
	return Rank(raws, foldString(query), cfg, limit)
}

// FoldQuery exposes the query-normalisation step so callers can cache it
// alongside their pre-computed raw matches.
func FoldQuery(query string) string { return foldString(query) }

// PositionsFor runs junegunn's optimal-alignment matcher against a single
// string and returns the matched rune indexes — used by the API-merge
// path to highlight chars on rows the local index didn't already score.
// Returns nil if the query doesn't match name at all.
func PositionsFor(query, name string) []int {
	if query == "" || name == "" {
		return nil
	}
	chars := util.ToChars([]byte(name))
	pattern := []rune(foldString(query))
	slab := util.MakeSlab(slabSize16, slabSize32)
	_, pos := algo.FuzzyMatchV2(false, true, true, &chars, pattern, true, slab)
	if pos == nil {
		return nil
	}
	out := make([]int, len(*pos))
	copy(out, *pos)
	return out
}

// consecutivePairs counts how many matched positions sit adjacent to the
// next-matched one in alias order — i.e. how many runs of contiguous
// characters the fzf match contains. Position lists from FuzzyMatchV2 are
// emitted back-to-front, so we sort first.
func consecutivePairs(positions []int) int {
	if len(positions) < 2 {
		return 0
	}
	sorted := make([]int, len(positions))
	copy(sorted, positions)
	// Insertion sort — positions list is tiny (len of query).
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	count := 0
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1]+1 {
			count++
		}
	}
	return count
}

// slabSize16 and slabSize32 mirror the buffer sizes the fzf binary uses by
// default. They control the working memory of FuzzyMatchV2; the same slab
// can be reused across calls within one keystroke.
const (
	slabSize16 = 100 * 1024
	slabSize32 = 2048
)
