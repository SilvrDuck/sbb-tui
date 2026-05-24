// Binary sweep-consec sweeps ConsecutiveBonus values against the
// scenario suite to find the best trade-off between human label
// preference (high consec bonus) and synthetic MRR (low consec bonus).
//
// Usage:
//
//	go run ./cmd/sweep-consec
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/necrom4/sbb-tui/ui/stations"
)

type query struct {
	Text  string `json:"text"`
	Style string `json:"style"`
}

type endpoint struct {
	UIC     string  `json:"uic"`
	Queries []query `json:"queries"`
}

type scenario struct {
	From endpoint `json:"from"`
	To   endpoint `json:"to"`
}

type testCase struct {
	Q   query
	UIC string
}

func main() {
	idx, err := stations.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}

	raw, err := os.ReadFile("data/scenarios.json")
	if err != nil {
		fmt.Fprintln(os.Stderr, "scenarios:", err)
		os.Exit(1)
	}
	var scs []scenario
	if err := json.Unmarshal(raw, &scs); err != nil {
		fmt.Fprintln(os.Stderr, "parse:", err)
		os.Exit(1)
	}
	var tests []testCase
	for _, s := range scs {
		for _, q := range s.From.Queries {
			tests = append(tests, testCase{q, s.From.UIC})
		}
		for _, q := range s.To.Queries {
			tests = append(tests, testCase{q, s.To.UIC})
		}
	}
	fmt.Fprintf(os.Stderr, "loaded %d test cases\n", len(tests))

	cache := make(map[string][]stations.RawMatch)
	folded := make(map[string]string)
	for _, t := range tests {
		if _, ok := cache[t.Q.Text]; !ok {
			cache[t.Q.Text] = idx.SearchRaw(t.Q.Text)
			folded[t.Q.Text] = stations.FoldQuery(t.Q.Text)
		}
	}

	base := func(consec int) stations.ScoreConfig {
		return stations.ScoreConfig{
			PrefixBonus:      100,
			AbbrExactBonus:   200,
			ConsecutiveBonus: consec,
			ModeBonus: map[string]int{
				"TRAIN": 250, "METRO": 150, "TRAM": 200, "BOAT": 100, "BUS": 0,
			},
			ChairliftPenalty: 200,
		}
	}

	for _, consec := range []int{20, 40, 60, 80, 100, 150, 200, 300} {
		cfg := base(consec)
		var totalRR float64
		top1, top3 := 0, 0
		styleRR := make(map[string]float64)
		styleN := make(map[string]int)
		for _, t := range tests {
			matches := stations.Rank(cache[t.Q.Text], folded[t.Q.Text], cfg, 10)
			rank := 0
			for i, m := range matches {
				if m.Station.UIC == t.UIC {
					rank = i + 1
					break
				}
			}
			rr := 0.0
			if rank > 0 {
				rr = 1.0 / float64(rank)
				if rank == 1 {
					top1++
				}
				if rank <= 3 {
					top3++
				}
			}
			totalRR += rr
			styleRR[t.Q.Style] += rr
			styleN[t.Q.Style]++
		}
		mrr := totalRR / float64(len(tests))
		t1 := float64(top1) / float64(len(tests)) * 100
		t3 := float64(top3) / float64(len(tests)) * 100
		fmt.Printf("consec=%-3d  MRR=%.3f  top1=%4.1f%%  top3=%4.1f%%  ", consec, mrr, t1, t3)
		for _, s := range []string{"prefix", "substring", "abbreviation", "cross-language", "typo"} {
			fmt.Printf("%s=%.2f ", s, styleRR[s]/float64(styleN[s]))
		}
		fmt.Println()
	}
}
