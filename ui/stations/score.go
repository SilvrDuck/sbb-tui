package stations

// ScoreConfig collects the additive scoring constants applied on top of the
// raw fzf v2 score. Tuned against the scenario suite in cmd/fuzzy-eval; the
// defaults in DefaultScoreConfig are the spike baseline before tuning.
type ScoreConfig struct {
	// PrefixBonus is added when the first len(query) runes of the alias,
	// after diacritic + case folding, equal the query — i.e. the alias
	// starts with what the user typed.
	PrefixBonus int

	// AbbrExactBonus is added when the alias is a station abbreviation and
	// equals the query case-insensitively. Cheap multilingual anchor.
	AbbrExactBonus int

	// ConsecutiveBonus is added per consecutive matched-character pair on
	// top of fzf's own BonusConsecutive. Boosts contiguous matches because
	// we're picking station names, not paths — disjoint subsequence is
	// usually less meaningful here than in directory navigation.
	ConsecutiveBonus int

	// ModeBonus[mode] is added per match based on transport mode of the
	// station. BUS is conventionally 0 and serves as the anchor.
	ModeBonus map[string]int

	// ChairliftPenalty is subtracted from any CHAIRLIFT/CABLE_CAR/ELEVATOR
	// match — these are seldom what a user means.
	ChairliftPenalty int
}

// DefaultScoreConfig returns the initial baseline scoring weights.
// These are intentionally conservative; the tuner replaces them.
func DefaultScoreConfig() ScoreConfig {
	return ScoreConfig{
		PrefixBonus:    0,
		AbbrExactBonus: 0,
		ModeBonus: map[string]int{
			"TRAIN": 0,
			"METRO": 0,
			"TRAM":  0,
			"BOAT":  0,
			"BUS":   0,
		},
		ChairliftPenalty: 0,
	}
}

// modeBonus returns the bonus for a station's mode, or 0 if unmapped.
func (c ScoreConfig) modeBonus(mode string) int {
	switch mode {
	case "CHAIRLIFT", "CABLE_CAR", "ELEVATOR":
		return -c.ChairliftPenalty
	}
	if c.ModeBonus == nil {
		return 0
	}
	return c.ModeBonus[mode]
}

// ModeBonusFor exposes modeBonus so callers outside the package can score
// candidates produced by other sources (e.g. SBB API merge) the same way
// the matcher does.
func (c ScoreConfig) ModeBonusFor(mode string) int { return c.modeBonus(mode) }
