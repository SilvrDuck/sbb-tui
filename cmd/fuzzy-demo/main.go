// Binary fuzzy-demo is a throwaway REPL that exercises ui/stations against
// the embedded SBB dataset. It exists to validate matching, ranking, and
// latency before wiring fuzzy search into the TUI.
//
// Usage:
//   go run ./cmd/fuzzy-demo
// then type queries one per line; Ctrl-D to exit.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/necrom4/sbb-tui/ui/stations"
)

const topN = 8

func main() {
	t0 := time.Now()
	idx, err := stations.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "loaded %d stations in %s\n\n", len(idx.Stations()), time.Since(t0))

	sc := bufio.NewScanner(os.Stdin)
	fmt.Fprint(os.Stderr, "query> ")
	for sc.Scan() {
		q := strings.TrimSpace(sc.Text())
		if q == "" {
			fmt.Fprint(os.Stderr, "query> ")
			continue
		}
		t := time.Now()
		matches := idx.Search(q, topN, stations.DefaultScoreConfig())
		dur := time.Since(t)
		fmt.Printf("--- %q in %s (%d hits) ---\n", q, dur, len(matches))
		for i, m := range matches {
			fmt.Printf("%2d. %-32s %-8s %-6s fzf=%-5d bonus=%-4d final=%-5d  highlight=%v\n",
				i+1,
				render(m.AliasText, m.MatchedRunes),
				m.Station.Mode,
				m.Station.Abbr,
				m.FZFScore,
				m.Bonus,
				m.FinalScore,
				m.MatchedRunes,
			)
			if m.AliasText != m.Station.Name {
				fmt.Printf("    → %s\n", m.Station.Name)
			}
		}
		fmt.Fprint(os.Stderr, "\nquery> ")
	}
	fmt.Fprintln(os.Stderr)
}

// render wraps matched runes in [brackets] for human-readable highlight
// preview in the terminal — replaced by real lipgloss styling in the TUI.
func render(s string, idx []int) string {
	if len(idx) == 0 {
		return s
	}
	runes := []rune(s)
	hit := make(map[int]bool, len(idx))
	for _, i := range idx {
		hit[i] = true
	}
	var b strings.Builder
	for i, r := range runes {
		if hit[i] {
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
