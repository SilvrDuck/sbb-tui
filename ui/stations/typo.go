package stations

// damerauLevenshtein returns the edit distance between a and b, counting
// single-character insertions, deletions, substitutions, and adjacent
// transpositions ("teh" ↔ "the" = 1 edit, not 2). O(|a|·|b|) time and
// space. Both strings are expected to be short (station names, query
// inputs); for our index a Hirschberg-style space-optimised version
// would be unnecessary.
func damerauLevenshtein(a, b []rune) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Rolling rows isn't enough for transpositions; we keep three.
	prev2 := make([]int, lb+1)
	prev1 := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev1[j] = j
	}

	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = minInt3(
				cur[j-1]+1,        // insertion
				prev1[j]+1,        // deletion
				prev1[j-1]+cost,   // substitution
			)
			if i >= 2 && j >= 2 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				if t := prev2[j-2] + 1; t < cur[j] {
					cur[j] = t
				}
			}
		}
		prev2, prev1, cur = prev1, cur, prev2
	}
	return prev1[lb]
}

func minInt3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// TypoMatch is one alias that matched the query within an edit-distance
// threshold, when normal fzf returned no useful hit.
type TypoMatch struct {
	Station   Station
	AliasText string
	Distance  int
}

// SearchTypos runs Damerau-Levenshtein against every alias in the index
// whose folded first rune matches the query's first rune (a coarse but
// cheap pre-filter that cuts ~26× the work). Returns matches whose edit
// distance is ≤ maxDist. Intended as a fallback when SearchRaw produced
// no strong result.
func (i *Index) SearchTypos(query string, maxDist int) []TypoMatch {
	queryFolded := foldString(query)
	if queryFolded == "" || maxDist <= 0 {
		return nil
	}
	queryRunes := []rune(queryFolded)
	firstRune := queryRunes[0]

	bestPerUIC := make(map[string]TypoMatch, 64)
	for k := range i.aliases {
		a := &i.aliases[k]
		if len(a.folded) == 0 || rune(a.folded[0]) != firstRune {
			// Coarse first-letter filter — only valid for ASCII; for
			// non-ASCII first chars we still scan everything below.
			if firstRune <= 127 {
				continue
			}
		}
		aliasRunes := []rune(a.folded)
		// Quick length-based bail: if lengths differ by more than maxDist,
		// the edit distance must exceed maxDist.
		if abs(len(aliasRunes)-len(queryRunes)) > maxDist {
			continue
		}
		d := damerauLevenshtein(queryRunes, aliasRunes)
		if d > maxDist {
			continue
		}
		uic := a.station.UIC
		cur, exists := bestPerUIC[uic]
		if !exists || d < cur.Distance {
			bestPerUIC[uic] = TypoMatch{
				Station:   *a.station,
				AliasText: a.text,
				Distance:  d,
			}
		}
	}

	out := make([]TypoMatch, 0, len(bestPerUIC))
	for _, m := range bestPerUIC {
		out = append(out, m)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
