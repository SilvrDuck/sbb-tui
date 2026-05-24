// Binary fuzzy-eval evaluates the station matcher against a curated suite
// of commute scenarios. It reports Mean Reciprocal Rank, top-K accuracy,
// and a per-query-style breakdown, plus an SBB API baseline (cached on
// disk to avoid hammering the public endpoint).
//
// Workflow:
//
//	1. First run with -fetch-api populates data/api-baseline.json from
//	   the live SBB locations endpoint, gently rate-limited.
//	2. Subsequent runs read from the cache; no network.
//	3. -tune flips into random-search mode and prints the best config.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	mathrand "math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/necrom4/sbb-tui/ui/stations"
)

type scenarioQuery struct {
	Text     string `json:"text"`
	Style    string `json:"style"`
	Language string `json:"language,omitempty"`
}

type scenarioEndpoint struct {
	UIC     string          `json:"uic"`
	Name    string          `json:"name"`
	Queries []scenarioQuery `json:"queries"`
}

type scenario struct {
	ID      int              `json:"id"`
	Persona string           `json:"persona"`
	Region  string           `json:"region"`
	Pattern string           `json:"pattern"`
	From    scenarioEndpoint `json:"from"`
	To      scenarioEndpoint `json:"to"`
}

type apiResponse struct {
	Stations []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Icon string `json:"icon"`
	} `json:"stations"`
}

type testCase struct {
	Scenario   *scenario
	Endpoint   string // "from" or "to"
	Query      scenarioQuery
	TargetUIC  string
	TargetName string
}

func expandScenarios(scenarios []scenario) []testCase {
	var tests []testCase
	for i := range scenarios {
		s := &scenarios[i]
		for _, q := range s.From.Queries {
			tests = append(tests, testCase{
				Scenario: s, Endpoint: "from", Query: q,
				TargetUIC: s.From.UIC, TargetName: s.From.Name,
			})
		}
		for _, q := range s.To.Queries {
			tests = append(tests, testCase{
				Scenario: s, Endpoint: "to", Query: q,
				TargetUIC: s.To.UIC, TargetName: s.To.Name,
			})
		}
	}
	return tests
}

func rankLocal(matches []stations.Match, targetUIC string) int {
	for i, m := range matches {
		if m.Station.UIC == targetUIC {
			return i + 1
		}
	}
	return 0
}

func rankAPI(resp apiResponse, targetUIC string) int {
	for i, s := range resp.Stations {
		if s.ID == targetUIC {
			return i + 1
		}
	}
	return 0
}

func mrr(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	sum := 0.0
	for _, r := range ranks {
		if r > 0 && r <= k {
			sum += 1.0 / float64(r)
		}
	}
	return sum / float64(len(ranks))
}

func topKAccuracy(ranks []int, k int) float64 {
	if len(ranks) == 0 {
		return 0
	}
	n := 0
	for _, r := range ranks {
		if r > 0 && r <= k {
			n++
		}
	}
	return float64(n) / float64(len(ranks))
}

func loadAPIBaseline(path string) (map[string]apiResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]apiResponse{}, nil
		}
		return nil, err
	}
	var out map[string]apiResponse
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func saveAPIBaseline(path string, data map[string]apiResponse) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func fetchAPI(query string) (apiResponse, error) {
	u := "https://transport.opendata.ch/v1/locations?type=station&query=" + url.QueryEscape(query)
	resp, err := http.Get(u)
	if err != nil {
		return apiResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return apiResponse{}, fmt.Errorf("api returned %s", resp.Status)
	}
	var out apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return apiResponse{}, err
	}
	return out, nil
}

// rawCache stores the fzf SearchRaw output per unique query string, plus
// the folded query — so tuning can apply Rank with different configs
// without recomputing the expensive fzf pass.
type rawCache struct {
	raws   map[string][]stations.RawMatch
	folded map[string]string
}

func buildRawCache(idx *stations.Index, tests []testCase) *rawCache {
	uniq := make(map[string]struct{})
	for _, t := range tests {
		uniq[t.Query.Text] = struct{}{}
	}
	c := &rawCache{
		raws:   make(map[string][]stations.RawMatch, len(uniq)),
		folded: make(map[string]string, len(uniq)),
	}
	for q := range uniq {
		c.raws[q] = idx.SearchRaw(q)
		c.folded[q] = stations.FoldQuery(q)
	}
	return c
}

func evaluate(idx *stations.Index, tests []testCase, cfg stations.ScoreConfig, topK int) (mrrVal float64, ranks []int) {
	return evaluateCached(buildRawCache(idx, tests), tests, cfg, topK)
}

func evaluateCached(c *rawCache, tests []testCase, cfg stations.ScoreConfig, topK int) (mrrVal float64, ranks []int) {
	ranks = make([]int, len(tests))
	for i, t := range tests {
		raws := c.raws[t.Query.Text]
		matches := stations.Rank(raws, c.folded[t.Query.Text], cfg, topK)
		ranks[i] = rankLocal(matches, t.TargetUIC)
	}
	return mrr(ranks, topK), ranks
}

func reportBreakdown(label string, tests []testCase, ranks []int, topK int) {
	fmt.Printf("\n=== %s — by query style ===\n", label)
	styles := make(map[string][]int)
	for i, t := range tests {
		styles[t.Query.Style] = append(styles[t.Query.Style], ranks[i])
	}
	keys := make([]string, 0, len(styles))
	for k := range styles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, style := range keys {
		r := styles[style]
		fmt.Printf("  %-16s n=%-4d  MRR=%.3f  top-1=%5.1f%%  top-3=%5.1f%%  top-10=%5.1f%%\n",
			style, len(r),
			mrr(r, topK),
			topKAccuracy(r, 1)*100,
			topKAccuracy(r, 3)*100,
			topKAccuracy(r, topK)*100,
		)
	}

	fmt.Printf("\n=== %s — by commute pattern ===\n", label)
	patterns := make(map[string][]int)
	for i, t := range tests {
		patterns[t.Scenario.Pattern] = append(patterns[t.Scenario.Pattern], ranks[i])
	}
	keys = keys[:0]
	for k := range patterns {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, pattern := range keys {
		r := patterns[pattern]
		fmt.Printf("  %-20s n=%-4d  MRR=%.3f  top-1=%5.1f%%  top-3=%5.1f%%\n",
			pattern, len(r),
			mrr(r, topK),
			topKAccuracy(r, 1)*100,
			topKAccuracy(r, 3)*100,
		)
	}
}

// randomConfig samples one ScoreConfig from the tuning grid.
func randomConfig(rng *mathrand.Rand) stations.ScoreConfig {
	return stations.ScoreConfig{
		PrefixBonus:      rng.Intn(9) * 50,   // 0..400
		AbbrExactBonus:   rng.Intn(11) * 100, // 0..1000
		ConsecutiveBonus: rng.Intn(11) * 5,   // 0..50 per consecutive pair
		ModeBonus: map[string]int{
			"TRAIN": rng.Intn(11) * 50, // 0..500
			"METRO": rng.Intn(7) * 50,  // 0..300
			"TRAM":  rng.Intn(5) * 50,  // 0..200
			"BOAT":  rng.Intn(5) * 50,  // 0..200
			"BUS":   0,
		},
		ChairliftPenalty: rng.Intn(5) * 50, // 0..200
	}
}

func tune(c *rawCache, tests []testCase, samples int, topK int, seed int64) stations.ScoreConfig {
	rng := mathrand.New(mathrand.NewSource(seed))
	var bestCfg stations.ScoreConfig
	var bestMRR float64 = -1
	for i := 0; i < samples; i++ {
		cfg := randomConfig(rng)
		m, _ := evaluateCached(c, tests, cfg, topK)
		if m > bestMRR {
			bestMRR = m
			bestCfg = cfg
			fmt.Printf("  [%4d/%d] new best MRR=%.4f\n", i+1, samples, m)
		}
	}
	fmt.Printf("\nbest MRR after %d samples: %.4f\n", samples, bestMRR)
	return bestCfg
}

// coordinateDescent sweeps each parameter's grid in turn (others fixed), keeps
// the value that maximises MRR on the train set, and loops until a full pass
// yields no improvement. Cheap finish on top of random search.
func coordinateDescent(c *rawCache, tests []testCase, start stations.ScoreConfig, topK int) stations.ScoreConfig {
	cur := start
	curMRR, _ := evaluateCached(c, tests, cur, topK)
	fmt.Printf("  starting MRR=%.4f\n", curMRR)

	params := []struct {
		name string
		grid []int
		get  func() int
		set  func(int)
	}{
		{"PrefixBonus", []int{0, 50, 100, 150, 200, 250, 300, 400, 500, 700},
			func() int { return cur.PrefixBonus }, func(v int) { cur.PrefixBonus = v }},
		{"AbbrExactBonus", []int{0, 100, 200, 300, 500, 700, 900, 1200, 1500},
			func() int { return cur.AbbrExactBonus }, func(v int) { cur.AbbrExactBonus = v }},
		{"ConsecutiveBonus", []int{0, 5, 10, 15, 20, 25, 30, 40, 50, 75, 100},
			func() int { return cur.ConsecutiveBonus }, func(v int) { cur.ConsecutiveBonus = v }},
		{"TRAIN", []int{0, 50, 100, 150, 200, 250, 300, 400, 500},
			func() int { return cur.ModeBonus["TRAIN"] }, func(v int) { cur.ModeBonus["TRAIN"] = v }},
		{"METRO", []int{0, 50, 100, 150, 200, 250, 300},
			func() int { return cur.ModeBonus["METRO"] }, func(v int) { cur.ModeBonus["METRO"] = v }},
		{"TRAM", []int{0, 25, 50, 100, 150, 200},
			func() int { return cur.ModeBonus["TRAM"] }, func(v int) { cur.ModeBonus["TRAM"] = v }},
		{"BOAT", []int{0, 25, 50, 100, 150, 200},
			func() int { return cur.ModeBonus["BOAT"] }, func(v int) { cur.ModeBonus["BOAT"] = v }},
		{"ChairliftPenalty", []int{0, 25, 50, 100, 150, 200, 300},
			func() int { return cur.ChairliftPenalty }, func(v int) { cur.ChairliftPenalty = v }},
	}

	for pass := 1; ; pass++ {
		improved := false
		for _, p := range params {
			bestVal := p.get()
			for _, v := range p.grid {
				p.set(v)
				m, _ := evaluateCached(c, tests, cur, topK)
				if m > curMRR {
					curMRR = m
					bestVal = v
					improved = true
					fmt.Printf("  pass %d  %-18s := %-4d  MRR=%.4f\n", pass, p.name, v, m)
				}
			}
			p.set(bestVal)
		}
		if !improved {
			fmt.Printf("  pass %d: no improvement, stopping\n", pass)
			break
		}
	}

	fmt.Printf("\nfinal MRR after coordinate descent: %.4f\n", curMRR)
	return cur
}

// splitScenarios splits by scenario ID (not test-case), so all queries for the
// same station stay together — preventing trivial leakage.
func splitScenarios(scenarios []scenario, holdoutFrac float64, seed int64) (train, holdout []scenario) {
	rng := mathrand.New(mathrand.NewSource(seed))
	idx := rng.Perm(len(scenarios))
	nHold := int(float64(len(scenarios)) * holdoutFrac)
	if nHold < 1 && len(scenarios) > 4 {
		nHold = 1
	}
	for i, j := range idx {
		if i < nHold {
			holdout = append(holdout, scenarios[j])
		} else {
			train = append(train, scenarios[j])
		}
	}
	return
}

func main() {
	scenariosPath := flag.String("scenarios", "data/scenarios.json", "path to scenarios JSON")
	apiCachePath := flag.String("api-cache", "data/api-baseline.json", "path to API baseline cache")
	fetchAPIFlag := flag.Bool("fetch-api", false, "fetch missing API responses (uses network, gently rate-limited)")
	topK := flag.Int("top-k", 10, "top-K to evaluate against")
	tuneFlag := flag.Bool("tune", false, "random-search ScoreConfig")
	tuneSamples := flag.Int("tune-samples", 500, "number of random-search samples")
	descend := flag.Bool("descend", true, "after random search, run coordinate-descent refinement")
	holdoutFrac := flag.Float64("holdout", 0.2, "fraction of scenarios reserved for evaluation; 0 disables the split")
	seed := flag.Int64("seed", 42, "RNG seed for train/holdout split and random search")
	flag.Parse()

	scenariosBytes, err := os.ReadFile(*scenariosPath)
	if err != nil {
		log.Fatal(err)
	}
	var scenarios []scenario
	if err := json.Unmarshal(scenariosBytes, &scenarios); err != nil {
		log.Fatal(err)
	}

	tests := expandScenarios(scenarios)
	fmt.Printf("Loaded %d scenarios → %d test cases\n", len(scenarios), len(tests))

	idx, err := stations.Load()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Loaded %d stations\n", len(idx.Stations()))

	apiCache, err := loadAPIBaseline(*apiCachePath)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("API cache: %d entries\n", len(apiCache))

	if *fetchAPIFlag {
		missing := 0
		for _, t := range tests {
			if _, ok := apiCache[t.Query.Text]; !ok {
				missing++
			}
		}
		if missing > 0 {
			fmt.Printf("Fetching %d missing API entries (≈%ds at 200ms cadence)...\n",
				missing, missing/5)
			fetched := 0
			for _, t := range tests {
				if _, ok := apiCache[t.Query.Text]; ok {
					continue
				}
				resp, err := fetchAPI(t.Query.Text)
				if err != nil {
					log.Printf("api fetch %q: %v", t.Query.Text, err)
					continue
				}
				apiCache[t.Query.Text] = resp
				fetched++
				if fetched%25 == 0 {
					fmt.Printf("  %d / %d\n", fetched, missing)
				}
				time.Sleep(200 * time.Millisecond)
			}
			if err := saveAPIBaseline(*apiCachePath, apiCache); err != nil {
				log.Fatal(err)
			}
			fmt.Printf("Saved %d API responses to %s\n", len(apiCache), *apiCachePath)
		}
	}

	if *tuneFlag {
		var trainScenarios, holdoutScenarios []scenario
		if *holdoutFrac > 0 && len(scenarios) >= 5 {
			trainScenarios, holdoutScenarios = splitScenarios(scenarios, *holdoutFrac, *seed)
			fmt.Printf("\nSplit: %d train scenarios, %d holdout scenarios (seed=%d)\n",
				len(trainScenarios), len(holdoutScenarios), *seed)
		} else {
			trainScenarios = scenarios
			fmt.Printf("\nNo holdout split (too few scenarios or --holdout=0)\n")
		}
		trainTests := expandScenarios(trainScenarios)
		holdoutTests := expandScenarios(holdoutScenarios)

		cache := buildRawCache(idx, append(append([]testCase{}, trainTests...), holdoutTests...))

		fmt.Printf("\nRandom search over ScoreConfig (%d samples) on train set...\n", *tuneSamples)
		best := tune(cache, trainTests, *tuneSamples, *topK, *seed)

		if *descend {
			fmt.Printf("\nCoordinate-descent refinement on train set...\n")
			best = coordinateDescent(cache, trainTests, best, *topK)
		}

		fmt.Printf("\n--- Final config ---\n%+v\n", best)

		_, trainRanks := evaluate(idx, trainTests, best, *topK)
		fmt.Printf("\n=== Train (n=%d) ===\n", len(trainTests))
		fmt.Printf("MRR @ top-%d:    %.4f\n", *topK, mrr(trainRanks, *topK))
		fmt.Printf("Top-1 accuracy:  %5.1f%%\n", topKAccuracy(trainRanks, 1)*100)
		fmt.Printf("Top-3 accuracy:  %5.1f%%\n", topKAccuracy(trainRanks, 3)*100)
		fmt.Printf("Top-10 accuracy: %5.1f%%\n", topKAccuracy(trainRanks, *topK)*100)

		if len(holdoutTests) > 0 {
			_, holdRanks := evaluate(idx, holdoutTests, best, *topK)
			fmt.Printf("\n=== Holdout (n=%d) ===\n", len(holdoutTests))
			fmt.Printf("MRR @ top-%d:    %.4f\n", *topK, mrr(holdRanks, *topK))
			fmt.Printf("Top-1 accuracy:  %5.1f%%\n", topKAccuracy(holdRanks, 1)*100)
			fmt.Printf("Top-3 accuracy:  %5.1f%%\n", topKAccuracy(holdRanks, 3)*100)
			fmt.Printf("Top-10 accuracy: %5.1f%%\n", topKAccuracy(holdRanks, *topK)*100)
			reportBreakdown("holdout", holdoutTests, holdRanks, *topK)
		}
		return
	}

	cfg := stations.DefaultScoreConfig()
	_, localRanks := evaluate(idx, tests, cfg, *topK)

	apiRanks := make([]int, len(tests))
	apiHave := 0
	for i, t := range tests {
		if resp, ok := apiCache[t.Query.Text]; ok {
			apiRanks[i] = rankAPI(resp, t.TargetUIC)
			apiHave++
		}
	}

	fmt.Printf("\n=== Local matcher (DefaultScoreConfig) ===\n")
	fmt.Printf("MRR @ top-%d:    %.4f\n", *topK, mrr(localRanks, *topK))
	fmt.Printf("Top-1 accuracy:  %5.1f%%\n", topKAccuracy(localRanks, 1)*100)
	fmt.Printf("Top-3 accuracy:  %5.1f%%\n", topKAccuracy(localRanks, 3)*100)
	fmt.Printf("Top-10 accuracy: %5.1f%%\n", topKAccuracy(localRanks, *topK)*100)

	if apiHave > 0 {
		fmt.Printf("\n=== SBB API baseline (%d/%d cached) ===\n", apiHave, len(tests))
		fmt.Printf("MRR @ top-%d:    %.4f\n", *topK, mrr(apiRanks, *topK))
		fmt.Printf("Top-1 accuracy:  %5.1f%%\n", topKAccuracy(apiRanks, 1)*100)
		fmt.Printf("Top-3 accuracy:  %5.1f%%\n", topKAccuracy(apiRanks, 3)*100)
		fmt.Printf("Top-10 accuracy: %5.1f%%\n", topKAccuracy(apiRanks, *topK)*100)
	}

	reportBreakdown("local", tests, localRanks, *topK)
}
