package stations

import "sort"

// sortByFinalDesc sorts m in place by FinalScore descending, breaking ties
// by FZFScore then Station.Name for stable output.
func sortByFinalDesc(m []Match) {
	sort.Slice(m, func(i, j int) bool {
		if m[i].FinalScore != m[j].FinalScore {
			return m[i].FinalScore > m[j].FinalScore
		}
		if m[i].FZFScore != m[j].FZFScore {
			return m[i].FZFScore > m[j].FZFScore
		}
		return m[i].Station.Name < m[j].Station.Name
	})
}
