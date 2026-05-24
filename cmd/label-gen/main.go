// Binary label-gen emits a JSON file of A/B ranking pairs the user labels
// via scripts/label.html. Each example is one query + two candidate
// top-N ranked result lists produced under two different scoring
// strategies. The user picks A, B, or "no preference" — those labels
// become the supervision signal for the next round of tuning.
//
// Usage:
//
//	go run ./cmd/label-gen data/label-examples.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/necrom4/sbb-tui/ui/stations"
)

const topN = 6

// Result is one row inside an A or B candidate list, shaped for the
// HTML viewer. The viewer renders DisplayStr with the runes at
// DisplayPos highlighted; if Canonical != DisplayStr we show the
// "alias → canonical" two-column form.
type Result struct {
	UIC        string `json:"uic"`
	Mode       string `json:"mode"`
	Canonical  string `json:"canonical"`
	DisplayStr string `json:"displayStr"`
	DisplayPos []int  `json:"displayPos"`
	Score      int    `json:"score"`
}

// Example is one query-level labeling unit. ID is stable across regens
// so partial labels can be re-merged with newly added examples.
type Example struct {
	ID       int      `json:"id"`
	Query    string   `json:"query"`
	Category string   `json:"category"`
	Note     string   `json:"note,omitempty"`
	Variant  string   `json:"variant"`
	A        []Result `json:"a"`
	B        []Result `json:"b"`
}

// QuerySpec is one row in the curated example list — query plus the
// variant of "B" to try for it. A is always the current production
// scoring. Note is optional context shown to the labeler.
type QuerySpec struct {
	Q        string
	Cat      string
	Variant  string
	Note     string
}

// productionCfg mirrors ui.defaultScoreConfig — the current tuned baseline.
func productionCfg() stations.ScoreConfig {
	return stations.ScoreConfig{
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
}

// variantCfg returns the score config corresponding to a B-variant name.
// Each variant probes one dimension the labeler may care about.
func variantCfg(name string) stations.ScoreConfig {
	cfg := productionCfg()
	switch name {
	case "prefix-heavy":
		cfg.PrefixBonus = 800
	case "consecutive-heavy":
		cfg.ConsecutiveBonus = 200
	case "mode-flat":
		cfg.ModeBonus = map[string]int{"TRAIN": 0, "METRO": 0, "TRAM": 0, "BOAT": 0, "BUS": 0}
	case "train-only":
		cfg.ModeBonus = map[string]int{"TRAIN": 1000, "METRO": 0, "TRAM": 0, "BOAT": 0, "BUS": 0}
	case "no-bonus":
		cfg = stations.ScoreConfig{}
	case "abbr-heavy":
		cfg.AbbrExactBonus = 800
	case "chairlift-strong-penalty":
		cfg.ChairliftPenalty = 1500
	case "consecutive-strong":
		// Labeler feedback (2026-05-24): "prev rule was not too bad but
		// should heavily favor consecutive letters." This keeps the
		// production mode bonuses (so TRAIN still wins ties) while
		// bumping the per-pair consecutive bonus 15× so a fully
		// contiguous substring match clearly beats a scattered one.
		cfg.ConsecutiveBonus = 300
	}
	return cfg
}

// runConfig produces the top-N ranked matches for a given config.
func runConfig(idx *stations.Index, query string, cfg stations.ScoreConfig) []Result {
	raws := idx.SearchRaw(query)
	matches := stations.Rank(raws, stations.FoldQuery(query), cfg, topN)
	return toResults(matches)
}

// substringExactRerank is a special "B" path that ranks substring-exact
// matches first, then everything else by fzf's own score. Probes the
// "Arare" pathology where the canonical contains the query verbatim
// but ends up buried below scattered-letter matches.
func substringExactRerank(idx *stations.Index, query string) []Result {
	raws := idx.SearchRaw(query)
	folded := stations.FoldQuery(query)
	type ranked struct {
		raw    stations.RawMatch
		hasSub bool
		final  int
	}
	bestByUIC := make(map[string]ranked, len(raws))
	for i := range raws {
		r := raws[i]
		hasSub := strings.Contains(r.AliasFolded, folded)
		boost := 0
		if hasSub {
			boost = 10000
		}
		final := r.FZFScore + boost
		cur, ok := bestByUIC[r.UIC]
		if !ok || final > cur.final {
			bestByUIC[r.UIC] = ranked{raw: r, hasSub: hasSub, final: final}
		}
	}
	list := make([]ranked, 0, len(bestByUIC))
	for _, v := range bestByUIC {
		list = append(list, v)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].final > list[j].final })
	if len(list) > topN {
		list = list[:topN]
	}
	out := make([]Result, 0, len(list))
	for _, r := range list {
		canonical := r.raw.Station.Name
		display := r.raw.AliasText
		out = append(out, Result{
			UIC:        r.raw.UIC,
			Mode:       r.raw.Mode,
			Canonical:  canonical,
			DisplayStr: display,
			DisplayPos: r.raw.Positions,
			Score:      r.final,
		})
	}
	return out
}

func toResults(matches []stations.Match) []Result {
	out := make([]Result, 0, len(matches))
	for _, m := range matches {
		out = append(out, Result{
			UIC:        m.Station.UIC,
			Mode:       m.Station.Mode,
			Canonical:  m.Station.Name,
			DisplayStr: m.AliasText,
			DisplayPos: m.MatchedRunes,
			Score:      m.FinalScore,
		})
	}
	return out
}

// querySpecs is the curated list of 200 queries spanning every kind of
// ambiguity the labeler may want to express a preference on. Each entry
// pairs the query with the B-variant we compare the production config
// against. Variants rotate so each one collects ~25 labels.
//
// Categories:
//   - substring-exact: query is a contiguous substring of some station name
//     (Arare-pathology probes use the "substring-exact-rerank" variant)
//   - prefix: query matches the start of many stations
//   - crosslang-city: cross-language city label
//   - crosslang-transit: Flughafen / Aéroport / etc.
//   - abbr: SBB railway abbreviation
//   - multi-word: more than one whitespace-separated token
//   - typo: single-edit perturbations
//   - ambiguous: short / generic queries with many candidates
//   - followup: partial → realize ambiguity → restart (Mairie → Bernex)
func querySpecs() []QuerySpec {
	return []QuerySpec{
		// substring-exact (Arare-pathology probes) — 25 queries
		{"arare", "substring-exact", "substring-exact-rerank", "Plan-les-Ouates, Arare is an exact substring; should it surface?"},
		{"ouates", "substring-exact", "substring-exact-rerank", ""},
		{"cornavin", "substring-exact", "substring-exact-rerank", ""},
		{"eaux", "substring-exact", "substring-exact-rerank", ""},
		{"vives", "substring-exact", "substring-exact-rerank", ""},
		{"torfeld", "substring-exact", "substring-exact-rerank", ""},
		{"fluvial", "substring-exact", "substring-exact-rerank", ""},
		{"acacias", "substring-exact", "substring-exact-rerank", ""},
		{"halbinsel", "substring-exact", "substring-exact-rerank", ""},
		{"oerlikon", "substring-exact", "substring-exact-rerank", ""},
		{"stadelhofen", "substring-exact", "substring-exact-rerank", ""},
		{"enge", "substring-exact", "substring-exact-rerank", ""},
		{"altstetten", "substring-exact", "substring-exact-rerank", ""},
		{"wipkingen", "substring-exact", "substring-exact-rerank", ""},
		{"sécheron", "substring-exact", "substring-exact-rerank", ""},
		{"champel", "substring-exact", "substring-exact-rerank", ""},
		{"lancy", "substring-exact", "substring-exact-rerank", ""},
		{"meyrin", "substring-exact", "substring-exact-rerank", ""},
		{"prilly", "substring-exact", "substring-exact-rerank", ""},
		{"renens", "substring-exact", "substring-exact-rerank", ""},
		{"morges", "substring-exact", "substring-exact-rerank", ""},
		{"nyon", "substring-exact", "substring-exact-rerank", ""},
		{"vevey", "substring-exact", "substring-exact-rerank", ""},
		{"aigle", "substring-exact", "substring-exact-rerank", ""},
		{"montreux", "substring-exact", "substring-exact-rerank", ""},

		// prefix — 25 queries
		{"bern", "prefix", "prefix-heavy", ""},
		{"zur", "prefix", "prefix-heavy", ""},
		{"gen", "prefix", "prefix-heavy", ""},
		{"lau", "prefix", "prefix-heavy", ""},
		{"bas", "prefix", "prefix-heavy", ""},
		{"lug", "prefix", "prefix-heavy", ""},
		{"win", "prefix", "prefix-heavy", ""},
		{"luz", "prefix", "prefix-heavy", ""},
		{"stg", "prefix", "prefix-heavy", ""},
		{"fri", "prefix", "prefix-heavy", ""},
		{"neuc", "prefix", "prefix-heavy", ""},
		{"biel", "prefix", "prefix-heavy", ""},
		{"brig", "prefix", "prefix-heavy", ""},
		{"visp", "prefix", "prefix-heavy", ""},
		{"thun", "prefix", "prefix-heavy", ""},
		{"olt", "prefix", "prefix-heavy", ""},
		{"yverdon", "prefix", "prefix-heavy", ""},
		{"aarau", "prefix", "prefix-heavy", ""},
		{"chur", "prefix", "prefix-heavy", ""},
		{"dav", "prefix", "prefix-heavy", ""},
		{"int", "prefix", "prefix-heavy", "Interlaken vs many others"},
		{"sion", "prefix", "prefix-heavy", ""},
		{"sch", "prefix", "prefix-heavy", "Schaffhausen vs Schwarzenburg vs …"},
		{"will", "prefix", "prefix-heavy", "Wil SG"},
		{"romo", "prefix", "prefix-heavy", "Romont, Romoos…"},

		// crosslang-city — 25 queries
		{"genf", "crosslang-city", "consecutive-heavy", "Genève (de)"},
		{"geneva", "crosslang-city", "consecutive-heavy", "Genève (en)"},
		{"ginevra", "crosslang-city", "consecutive-heavy", "Genève (it)"},
		{"genevra", "crosslang-city", "consecutive-heavy", "Genève (rm)"},
		{"zurigo", "crosslang-city", "consecutive-heavy", "Zürich (it)"},
		{"zurich", "crosslang-city", "consecutive-heavy", "Zürich (folded ü)"},
		{"turitg", "crosslang-city", "consecutive-heavy", "Zürich (rm)"},
		{"berna", "crosslang-city", "consecutive-heavy", "Bern (it/rm)"},
		{"berne", "crosslang-city", "consecutive-heavy", "Bern (fr)"},
		{"basilea", "crosslang-city", "consecutive-heavy", "Basel (it)"},
		{"bâle", "crosslang-city", "consecutive-heavy", "Basel (fr)"},
		{"losanna", "crosslang-city", "consecutive-heavy", "Lausanne (it)"},
		{"sitten", "crosslang-city", "consecutive-heavy", "Sion (de)"},
		{"viège", "crosslang-city", "consecutive-heavy", "Visp (fr)"},
		{"viege", "crosslang-city", "consecutive-heavy", "Visp (fr, no diacritic)"},
		{"brigue", "crosslang-city", "consecutive-heavy", "Brig (fr)"},
		{"briga", "crosslang-city", "consecutive-heavy", "Brig (it)"},
		{"san gallo", "crosslang-city", "consecutive-heavy", "St. Gallen (it)"},
		{"saint-gall", "crosslang-city", "consecutive-heavy", "St. Gallen (fr)"},
		{"coira", "crosslang-city", "consecutive-heavy", "Chur (it)"},
		{"coire", "crosslang-city", "consecutive-heavy", "Chur (fr)"},
		{"lucerne", "crosslang-city", "consecutive-heavy", "Luzern (fr/en)"},
		{"lugano", "crosslang-city", "consecutive-heavy", ""},
		{"friburgo", "crosslang-city", "consecutive-heavy", "Fribourg (it)"},
		{"locarno", "crosslang-city", "consecutive-heavy", ""},

		// crosslang-transit (airport / station class) — 15 queries
		{"flughafen", "crosslang-transit", "consecutive-heavy", "wants Zürich Flughafen / Genève-Aéroport"},
		{"airport", "crosslang-transit", "consecutive-heavy", ""},
		{"aéroport", "crosslang-transit", "consecutive-heavy", ""},
		{"aeroport", "crosslang-transit", "consecutive-heavy", "no diacritic"},
		{"aeroporto", "crosslang-transit", "consecutive-heavy", ""},
		{"bahnhof", "crosslang-transit", "consecutive-heavy", ""},
		{"gare", "crosslang-transit", "consecutive-heavy", "very common — many matches"},
		{"stazione", "crosslang-transit", "consecutive-heavy", ""},
		{"hauptbahnhof", "crosslang-transit", "consecutive-heavy", ""},
		{"hbf", "crosslang-transit", "consecutive-heavy", ""},
		{"hb", "crosslang-transit", "consecutive-heavy", "Zurich HB et al."},
		{"sbb", "crosslang-transit", "consecutive-heavy", ""},
		{"cff", "crosslang-transit", "consecutive-heavy", ""},
		{"ffs", "crosslang-transit", "consecutive-heavy", ""},
		{"station", "crosslang-transit", "consecutive-heavy", ""},

		// abbreviations — 15 queries
		{"ge", "abbr", "abbr-heavy", "Genève abbr"},
		{"zh", "abbr", "abbr-heavy", "no SBB abbr — folded match"},
		{"vd", "abbr", "abbr-heavy", ""},
		{"be", "abbr", "abbr-heavy", "Bern abbr"},
		{"bs", "abbr", "abbr-heavy", "Basel"},
		{"lu", "abbr", "abbr-heavy", "Luzern"},
		{"ne", "abbr", "abbr-heavy", "Neuchâtel"},
		{"ti", "abbr", "abbr-heavy", ""},
		{"ar", "abbr", "abbr-heavy", ""},
		{"sg", "abbr", "abbr-heavy", "St Gallen"},
		{"ow", "abbr", "abbr-heavy", ""},
		{"nw", "abbr", "abbr-heavy", ""},
		{"jb", "abbr", "abbr-heavy", ""},
		{"bgla", "abbr", "abbr-heavy", "Berner Oberland Bahn?"},
		{"vt", "abbr", "abbr-heavy", ""},

		// multi-word — 20 queries
		{"zürich hb", "multi-word", "prefix-heavy", ""},
		{"zurich hb", "multi-word", "prefix-heavy", "no diacritic"},
		{"geneva airport", "multi-word", "consecutive-heavy", ""},
		{"genève aéroport", "multi-word", "consecutive-heavy", ""},
		{"bern bahnhof", "multi-word", "consecutive-heavy", ""},
		{"basel sbb", "multi-word", "prefix-heavy", ""},
		{"lausanne gare", "multi-word", "consecutive-heavy", ""},
		{"zurich hauptbahnhof", "multi-word", "consecutive-heavy", ""},
		{"bellinzona stazione", "multi-word", "consecutive-heavy", ""},
		{"chêne bourg", "multi-word", "consecutive-heavy", ""},
		{"chene bourg", "multi-word", "consecutive-heavy", "no diacritic"},
		{"st gallen", "multi-word", "prefix-heavy", ""},
		{"plan les ouates", "multi-word", "substring-exact-rerank", ""},
		{"bern hb", "multi-word", "prefix-heavy", ""},
		{"luzern bahn", "multi-word", "consecutive-heavy", ""},
		{"interlaken ost", "multi-word", "prefix-heavy", ""},
		{"interlaken west", "multi-word", "prefix-heavy", ""},
		{"olten hb", "multi-word", "prefix-heavy", ""},
		{"chur bahn", "multi-word", "consecutive-heavy", ""},
		{"davos platz", "multi-word", "prefix-heavy", ""},

		// typos — 20 queries
		{"lausnane", "typo", "no-bonus", "swapped letters"},
		{"luzren", "typo", "no-bonus", ""},
		{"barsel", "typo", "no-bonus", "Basel"},
		{"berm", "typo", "no-bonus", "Bern m vs n"},
		{"wintehrtur", "typo", "no-bonus", "Winterthur"},
		{"loausanne", "typo", "no-bonus", ""},
		{"zhrich", "typo", "no-bonus", "missing letter"},
		{"genvee", "typo", "no-bonus", ""},
		{"corvanin", "typo", "no-bonus", "Cornavin transposed"},
		{"lugnao", "typo", "no-bonus", "Lugano"},
		{"sittn", "typo", "no-bonus", ""},
		{"olton", "typo", "no-bonus", "Olten"},
		{"yverdom", "typo", "no-bonus", "Yverdon m"},
		{"breig", "typo", "no-bonus", "Brig"},
		{"chesseau", "typo", "no-bonus", "Chexbres? approximation"},
		{"montruex", "typo", "no-bonus", "Montreux transposed"},
		{"sionn", "typo", "no-bonus", ""},
		{"frinurg", "typo", "no-bonus", ""},
		{"vauey", "typo", "no-bonus", "Vevey"},
		{"thunn", "typo", "no-bonus", ""},

		// ambiguous (short / generic) — 25 queries
		{"saint", "ambiguous", "mode-flat", "tons of St-* stations"},
		{"st", "ambiguous", "mode-flat", ""},
		{"st-", "ambiguous", "mode-flat", ""},
		{"san", "ambiguous", "mode-flat", ""},
		{"santa", "ambiguous", "mode-flat", ""},
		{"le", "ambiguous", "train-only", "very generic"},
		{"la", "ambiguous", "train-only", ""},
		{"les", "ambiguous", "train-only", ""},
		{"haute", "ambiguous", "train-only", ""},
		{"rue", "ambiguous", "train-only", ""},
		{"place", "ambiguous", "mode-flat", ""},
		{"av", "ambiguous", "train-only", "avenue prefix"},
		{"centre", "ambiguous", "train-only", ""},
		{"gare", "ambiguous", "train-only", "tons of *, gare bus stops"},
		{"haltes", "ambiguous", "train-only", ""},
		{"poste", "ambiguous", "train-only", ""},
		{"école", "ambiguous", "train-only", ""},
		{"ecole", "ambiguous", "train-only", ""},
		{"hôpital", "ambiguous", "train-only", ""},
		{"hopital", "ambiguous", "train-only", ""},
		{"village", "ambiguous", "train-only", ""},
		{"dorf", "ambiguous", "train-only", "DE village suffix"},
		{"pont", "ambiguous", "train-only", ""},
		{"port", "ambiguous", "train-only", ""},
		{"lac", "ambiguous", "train-only", ""},

		// followup (partial-then-restart pattern) — 15 queries
		{"mairie", "followup", "mode-flat", "user types this, sees Bernex result, will restart with 'bernex'"},
		{"bernex", "followup", "substring-exact-rerank", "follow-up to 'mairie' realisation"},
		{"plac", "followup", "substring-exact-rerank", "abandoned 'place ...'"},
		{"av d", "followup", "consecutive-heavy", "abandoned 'av de la ...'"},
		{"hopit", "followup", "substring-exact-rerank", ""},
		{"ec", "followup", "prefix-heavy", "abandoned ecole"},
		{"par", "followup", "prefix-heavy", "parc? parcours?"},
		{"port", "followup", "substring-exact-rerank", ""},
		{"vil", "followup", "prefix-heavy", "villars? villeret?"},
		{"chât", "followup", "consecutive-heavy", "château…"},
		{"chat", "followup", "consecutive-heavy", "no diacritic"},
		{"rive", "followup", "substring-exact-rerank", ""},
		{"sport", "followup", "substring-exact-rerank", ""},
		{"piscine", "followup", "substring-exact-rerank", ""},
		{"stade", "followup", "substring-exact-rerank", ""},

		// edge cases — 15 queries
		{"a", "ambiguous", "train-only", "1-char — should we even show results?"},
		{"e", "ambiguous", "train-only", "1-char"},
		{"-", "ambiguous", "train-only", "punctuation only"},
		{".", "ambiguous", "train-only", ""},
		{"  ", "ambiguous", "train-only", "whitespace only"},
		{"xyz", "ambiguous", "train-only", "no real match expected"},
		{"qqq", "ambiguous", "train-only", ""},
		{"zürich flughafen", "multi-word", "substring-exact-rerank", ""},
		{"zh flughafen", "multi-word", "consecutive-heavy", ""},
		{"ge aero", "multi-word", "consecutive-heavy", ""},
		{"zürich oerlikon", "multi-word", "substring-exact-rerank", ""},
		{"lausanne flon", "multi-word", "substring-exact-rerank", ""},
		{"genève cornavin", "multi-word", "substring-exact-rerank", ""},
		{"bern hbf", "multi-word", "consecutive-heavy", ""},
		{"basel bad", "multi-word", "prefix-heavy", "Basel Badischer Bahnhof"},

		// v2 probes — appended 2026-05-24 after labeler said the
		// substring-exact-rerank variant dropped the TRAIN canonical
		// too aggressively (Renens VD wasn't first). consecutive-strong
		// keeps production mode bonuses and just amplifies the per-pair
		// consecutive bonus. These re-test the same query set so labels
		// directly compare "drop mode, surface all substrings" against
		// "keep mode, just favor consecutive". IDs start at 201.
		{"arare", "substring-exact-v2", "consecutive-strong", "v2: keep mode bonuses, boost consecutive — does Renens-VD-style canonical come back?"},
		{"ouates", "substring-exact-v2", "consecutive-strong", "v2"},
		{"cornavin", "substring-exact-v2", "consecutive-strong", "v2"},
		{"eaux", "substring-exact-v2", "consecutive-strong", "v2"},
		{"vives", "substring-exact-v2", "consecutive-strong", "v2"},
		{"torfeld", "substring-exact-v2", "consecutive-strong", "v2"},
		{"fluvial", "substring-exact-v2", "consecutive-strong", "v2"},
		{"acacias", "substring-exact-v2", "consecutive-strong", "v2"},
		{"halbinsel", "substring-exact-v2", "consecutive-strong", "v2"},
		{"oerlikon", "substring-exact-v2", "consecutive-strong", "v2"},
		{"stadelhofen", "substring-exact-v2", "consecutive-strong", "v2"},
		{"enge", "substring-exact-v2", "consecutive-strong", "v2"},
		{"altstetten", "substring-exact-v2", "consecutive-strong", "v2"},
		{"wipkingen", "substring-exact-v2", "consecutive-strong", "v2"},
		{"sécheron", "substring-exact-v2", "consecutive-strong", "v2"},
		{"champel", "substring-exact-v2", "consecutive-strong", "v2"},
		{"lancy", "substring-exact-v2", "consecutive-strong", "v2"},
		{"meyrin", "substring-exact-v2", "consecutive-strong", "v2"},
		{"prilly", "substring-exact-v2", "consecutive-strong", "v2"},
		{"renens", "substring-exact-v2", "consecutive-strong", "v2 — the case that triggered this variant"},
		{"morges", "substring-exact-v2", "consecutive-strong", "v2"},
		{"nyon", "substring-exact-v2", "consecutive-strong", "v2"},
		{"vevey", "substring-exact-v2", "consecutive-strong", "v2"},
		{"aigle", "substring-exact-v2", "consecutive-strong", "v2"},
		{"montreux", "substring-exact-v2", "consecutive-strong", "v2"},
		// also re-test the ambiguous and followup cases where the
		// "TRAIN canonical first" question matters most
		{"bernex", "followup-v2", "consecutive-strong", "v2"},
		{"rive", "followup-v2", "consecutive-strong", "v2"},
		{"stade", "followup-v2", "consecutive-strong", "v2"},
		{"sport", "followup-v2", "consecutive-strong", "v2"},
		{"piscine", "followup-v2", "consecutive-strong", "v2"},
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: label-gen <out.json>")
		os.Exit(2)
	}
	outPath := os.Args[1]

	idx, err := stations.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	specs := querySpecs()
	prodCfg := productionCfg()

	examples := make([]Example, 0, len(specs))
	skipped := 0
	for i, sp := range specs {
		a := runConfig(idx, sp.Q, prodCfg)
		var b []Result
		if sp.Variant == "substring-exact-rerank" {
			b = substringExactRerank(idx, sp.Q)
		} else {
			b = runConfig(idx, sp.Q, variantCfg(sp.Variant))
		}
		// If A and B are identical (no preference signal), skip the
		// example — the labeler would have no choice to make.
		if equalResults(a, b) || (len(a) == 0 && len(b) == 0) {
			skipped++
			continue
		}
		examples = append(examples, Example{
			ID:       i + 1,
			Query:    sp.Q,
			Category: sp.Cat,
			Variant:  sp.Variant,
			Note:     sp.Note,
			A:        a,
			B:        b,
		})
	}

	f, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(examples); err != nil {
		fmt.Fprintln(os.Stderr, "encode:", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr,
		"wrote %d examples to %s (skipped %d identical-A=B from %d specs)\n",
		len(examples), outPath, skipped, len(specs),
	)
}

// equalResults compares two result lists ignoring score (since A and B
// have different scoring scales — what matters is the ordering and the
// chosen stations).
func equalResults(a, b []Result) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].UIC != b[i].UIC || a[i].DisplayStr != b[i].DisplayStr {
			return false
		}
	}
	return true
}
