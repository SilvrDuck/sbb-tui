// Package ui implements the Bubbletea TUI for SBB timetable queries.
package ui

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/necrom4/sbb-tui/ui/stations"
)

var (
	//go:embed sbb-logo.txt
	sbbLogo string

	//go:embed sbb-logo-nerdfont.txt
	sbbLogoNerdFont string

	latestReleaseURL = "https://github.com/Necrom4/sbb-tui/releases/latest"
)

// View implements tea.Model.
func (m appModel) View() string {
	if m.width < minTermWidth || m.height < minTermHeight {
		msg := fmt.Sprintf("Terminal too small (%dx%d)\nMinimum size: %dx%d", m.width, m.height, minTermWidth, minTermHeight)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center,
			m.styles.warningBold.Render(msg))
	}

	header := m.renderHeader()
	footer := m.renderFooter()

	var popoverBlock string
	if m.popover != nil {
		// Compact the popover when the results area below already has
		// content — leaves room for at least one full connection card.
		rows := popoverRows
		if len(m.connections) > 0 {
			rows = popoverRowsCompact
		}
		popoverBlock = m.renderPopover(rows)
	}

	resultsH := m.resultsHeight()
	// Note: with z-index overlay (below), the body stays at full results
	// height regardless of whether the popover is open — the popover is
	// drawn *on top of* the body, not pushing it down.

	// Body block at full results height. The popover (if any) is overlaid
	// on top so the start-screen logo / connections list show through the
	// parts the popover does not cover — true z-indexing via ANSI splice.
	bodyBlock := lipgloss.JoinHorizontal(
		lipgloss.Top,
		m.styles.text.Height(resultsH).Render(m.renderResults()),
		m.styles.text.Height(resultsH).Render(m.renderDetailedResult()),
	)
	bodyBlock = clipToHeight(bodyBlock, resultsH)

	if popoverBlock != "" {
		bodyBlock = m.overlayLines(bodyBlock, popoverBlock)
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, bodyBlock, footer)
}

// overlayLines splices the overlay block over the base, line by line, so
// that portions of each base line outside the overlay's horizontal extent
// remain visible. Both blocks may carry ANSI styles; ansi.Cut preserves
// them.
func (m appModel) overlayLines(base, overlay string) string {
	if overlay == "" {
		return base
	}
	baseLines := strings.Split(base, "\n")
	ovLines := strings.Split(overlay, "\n")
	for i, ovLine := range ovLines {
		if i >= len(baseLines) {
			break
		}
		baseLines[i] = spliceOver(baseLines[i], ovLine, m.width)
	}
	return strings.Join(baseLines, "\n")
}

// spliceOver replaces a portion of base with overlay, derived from the
// overlay's leading-space count (used as left offset) and its visible width
// (used as the right edge of the covered region). Outside that span, base
// shows through.
func spliceOver(base, overlay string, totalWidth int) string {
	if overlay == "" {
		return base
	}
	leftOffset := 0
	for _, r := range overlay {
		if r != ' ' {
			break
		}
		leftOffset++
	}
	ovWidth := lipgloss.Width(overlay)
	if ovWidth <= leftOffset {
		return base
	}
	leftPart := ansi.Cut(base, 0, leftOffset)
	ovContent := ansi.Cut(overlay, leftOffset, ovWidth)
	rightPart := ansi.Cut(base, ovWidth, totalWidth)
	return leftPart + ovContent + rightPart
}

// clipToHeight truncates s to at most n lines.
func clipToHeight(s string, n int) string {
	if n <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n")
}

// popoverRowsCompact is the row count used when the popover floats above
// a results list — keeps a connection card visible below.
const popoverRowsCompact = 4

// ----- Layout calculations -----

// contentWidth returns the usable horizontal width of the TUI.
func (m appModel) contentWidth() int {
	return max(m.width, 0)
}

// resultsHeight returns the vertical space available for the results pane.
func (m appModel) resultsHeight() int {
	return max(m.height-headerHeight-helpBarHeight, 0)
}

// maxVisibleConnections is the count of result rows that fit in the results pane.
func (m appModel) maxVisibleConnections() int {
	return max(m.resultsHeight()/simpleConnHeight, 1)
}

// resultBoxWidth returns the width of one result column (simple list or detail).
func (m appModel) resultBoxWidth() int {
	return max((m.width-simpleConnMargin)/2, resultMargin+stopsLineMinWidth+stopsLineFixedWidth)
}

// headerFixedWidth returns the total horizontal space taken by the
// header items, treating the From/To inputs as their per-item overhead
// only so the leftover width can be split between them.
func (m appModel) headerFixedWidth() int {
	width := 0
	for i, item := range m.headerOrder {
		if item.id == "from" || item.id == "to" {
			width += borderSize + 2 + lipgloss.Width(m.inputs[item.index].Prompt)
			continue
		}
		width += lipgloss.Width(m.renderHeaderItem(i))
	}
	return width
}

// ----- Header rendering -----

// renderHeader joins every header item horizontally.
func (m appModel) renderHeader() string {
	var headerItems []string
	for i := range m.headerOrder {
		headerItems = append(headerItems, m.renderHeaderItem(i))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, headerItems...)
}

// ----- Footer rendering -----

// renderHelpBar returns the bottom-bar key hints. When the focused From/To
// input is in refocus-overwrite state, the bindings shift to reflect what
// each key does in that state (Enter keeps, → appends, typing rewrites).
func (m appModel) renderHelpBar() string {
	overwriteActive := false
	if a := m.headerOrder[m.tabIndex]; a.kind == kindInput && (a.index == 0 || a.index == 1) && m.overwriteOnType[a.index] {
		overwriteActive = true
	}

	var bindings []struct{ key, desc string }
	if overwriteActive {
		bindings = []struct{ key, desc string }{
			{m.icons.keyEnter, "keep"},
			{m.icons.keyRight, "append"},
			{"abc", "rewrite"},
			{m.icons.keyTab, "next"},
			{m.icons.keyEsc, "cancel"},
		}
	} else {
		bindings = []struct{ key, desc string }{
			{m.icons.keyTab, "navigate"},
			{m.icons.keyEnter, "search"},
			{m.icons.keySpace, "toggle"},
			{m.icons.keyUpDw, "results"},
			{m.icons.keyUPDW, "scroll"},
			{m.icons.keyRight, "complete"},
			{m.icons.keyEsc, "quit"},
		}
	}

	parts := make([]string, len(bindings))
	for i, b := range bindings {
		parts[i] = m.styles.helpKey.Render(b.key) + " " + m.styles.helpDesc.Render(b.desc)
	}

	return " " + strings.Join(parts, "   ")
}

// renderVersionBadge returns the bottom-right "SBB-TUI vX.Y.Z" badge,
// shrinking or hiding itself when availableWidth is too narrow.
func (m appModel) renderVersionBadge(availableWidth int) string {
	const (
		appName = "SBB-TUI"
		minGap  = 2
	)

	if availableWidth <= minGap {
		return ""
	}

	if m.newerVersion != "" {
		full := fmt.Sprintf(
			"%s %s %s%s%s",
			m.styles.text.Render(appName),
			m.styles.textMuted.Render(m.currentVersion),
			m.styles.warning.Render("(latest: "),
			m.styles.warning.Render(renderLink(m.newerVersion, latestReleaseURL)),
			m.styles.warning.Render(")"),
		)
		if lipgloss.Width(full)+minGap <= availableWidth {
			return full
		}
	}

	short := fmt.Sprintf(
		"%s %s",
		m.styles.text.Render(appName),
		m.styles.textMuted.Render(m.currentVersion),
	)

	if lipgloss.Width(short)+minGap <= availableWidth {
		return short
	}

	return ""
}

// renderFooter combines the help bar and the version badge with stretching whitespace.
func (m appModel) renderFooter() string {
	helpBar := m.renderHelpBar()
	versionBadge := m.renderVersionBadge(m.width - lipgloss.Width(helpBar))

	if versionBadge == "" {
		return helpBar
	}

	gap := m.width - lipgloss.Width(helpBar) - lipgloss.Width(versionBadge)
	return helpBar + strings.Repeat(" ", gap) + versionBadge
}

// renderHeaderItem renders a single header item (input or button) styled
// per its focus state.
func (m appModel) renderHeaderItem(idx int) string {
	item := m.headerOrder[idx]
	style := m.styles.inactive
	if m.tabIndex == idx {
		style = m.styles.active
	}

	if item.kind == kindInput {
		input := m.inputs[item.index]
		view := input.View()
		// Always clip to the intended width. The textinput widget emits an
		// extra trailing cursor cell regardless of focus state, and any
		// suggestion ghost would also overflow. Without this clip the From/To
		// boxes grow by 1 col each, pushing the rightmost header button
		// (search ⌕) off the screen.
		maxView := lipgloss.Width(input.Prompt) + input.Width
		view = ansi.Truncate(view, maxView, "")
		return style.Render(view)
	}

	icon := " "
	switch item.id {
	case "swap":
		icon = m.icons.swap
	case "isArrivalTime":
		if m.isArrivalTime {
			icon = m.icons.arrival
		} else {
			icon = m.icons.departure
		}
	case "search":
		icon = m.icons.search
	}
	return style.Render(icon)
}

// ----- Results layout -----

// renderResults dispatches to the loading/error/start/list views.
func (m appModel) renderResults() string {
	if m.loading {
		return m.renderLoading()
	}

	if m.errorMsg != nil {
		return "\n  " + m.styles.warning.Render(userError(m.errorMsg))
	}

	if len(m.connections) == 0 {
		if m.searched {
			return "\n  No connections found."
		}
		return m.renderStartScreen()
	}

	var boxes []string
	boxWidth := m.resultBoxWidth()

	for i, c := range m.connections {
		boxes = append(boxes, m.renderSimpleConnection(c, i, boxWidth))
	}

	return lipgloss.JoinVertical(lipgloss.Left, boxes...)
}

// onStartScreen reports whether the start screen (logo + tagline) is currently shown.
func (m appModel) onStartScreen() bool {
	return m.errorMsg == nil && !m.loading && len(m.connections) == 0 && !m.searched
}

// renderStartScreen renders the centered logo, tagline and optional update notice.
func (m appModel) renderStartScreen() string {
	return m.renderStartScreenSized(m.resultsHeight())
}

// renderStartScreenSized renders the start screen into a specific height so
// the logo stays visible (just squeezed) when the popover is taking room.
func (m appModel) renderStartScreenSized(height int) string {
	logo := sbbLogo
	if m.nerdFont {
		logo = sbbLogoNerdFont
	}
	logo = strings.TrimRight(logo, "\n")

	coloredLogo := m.renderLogo(logo)
	text := m.renderStartTagline("Enter stations above to see timetables")

	block := lipgloss.JoinVertical(lipgloss.Center, text, "", coloredLogo)

	if m.newerVersion != "" {
		latestVersion := renderLink(m.newerVersion, latestReleaseURL)
		label := fmt.Sprintf("Update available: %s", latestVersion)
		block = lipgloss.JoinVertical(lipgloss.Center, block, "", m.styles.active.Render(label))
	}

	width := max(m.contentWidth(), 0)

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, block)
}

// renderPopover renders the fuzzy-picker overlay as a bordered panel
// anchored to the focused input's left edge. Column layout (per the
// spec):
//
//   - Popover left border at the focused field's left edge.
//   - Alias text left-aligned, starts at the focused field's value
//     column (approximated as field-left + 4 to clear border/padding/
//     prompt — independent of prompt-width quirks).
//   - Arrow at the left edge of the next header field.
//   - Canonical left-aligned, starts at arrow + 2.
//   - Mode badge at a fixed distance from the arrow (computed to fit
//     the worst-case anchor without overflowing m.width).
//   - Right border just after the mode badge.
//
// maxRows caps how many matches are shown so the panel fits over a busy
// results pane without overflowing the screen.
func (m appModel) renderPopover(maxRows int) string {
	p := m.popover

	const (
		valueOffsetInBox   = 4  // approximate col of value text inside an input box
		modeBadgeW         = 9  // wide enough for "CHAIRLIFT"
		preferredFixedDist = 30 // arrow-to-mode distance, in cols
		minFixedDist       = 14 // floor: arrow + " canon  MODE"
		gap                = 1  // 1-col gap between segments
	)

	// Find focused input's position in the header.
	focusedHeaderIdx := 0
	for i, item := range m.headerOrder {
		if item.kind == kindInput && item.index == p.inputIdx {
			focusedHeaderIdx = i
			break
		}
	}

	// Screen column where the focused input box begins.
	popoverLeft := 0
	for i := 0; i < focusedHeaderIdx; i++ {
		popoverLeft += lipgloss.Width(m.renderHeaderItem(i))
	}
	focusedBoxW := lipgloss.Width(m.renderHeaderItem(focusedHeaderIdx))

	// Fixed-distance must accommodate the worst-case anchor — the To
	// popover sits further right, so use From+To widths for the bound.
	// This keeps the visual structure consistent whichever field is focused.
	worstAnchor := lipgloss.Width(m.renderHeaderItem(0)) + lipgloss.Width(m.renderHeaderItem(1))
	maxFixedDist := m.width - worstAnchor - modeBadgeW - 1 // -1 for right border
	fixedDist := preferredFixedDist
	if fixedDist > maxFixedDist {
		fixedDist = maxFixedDist
	}
	if fixedDist < minFixedDist {
		fixedDist = minFixedDist
	}

	// Screen columns of each landmark.
	arrowScreenCol := popoverLeft + focusedBoxW
	modeScreenStart := arrowScreenCol + fixedDist
	popoverRight := modeScreenStart + modeBadgeW + 1 // +1 for right border
	if popoverRight > m.width {
		popoverRight = m.width
	}
	popoverWidth := popoverRight - popoverLeft

	box := m.styles.active.UnsetPadding().Padding(0, 0).Width(popoverWidth - borderSize)

	highlightStyle := lipgloss.NewStyle().Foreground(m.styles.active.GetBorderTopForeground()).Bold(true)
	canonStyle := m.styles.text.Bold(true)
	modeStyle := m.styles.textMuted
	rowStyle := m.styles.text
	markerStyle := lipgloss.NewStyle().Foreground(m.styles.active.GetBorderTopForeground())

	// Column positions relative to popover content frame (= screen col - popoverLeft - 1).
	const contentBorder = 1
	aliasCol := valueOffsetInBox - contentBorder // = 3 in content frame
	arrowCol := arrowScreenCol - popoverLeft - contentBorder
	canonCol := arrowCol + 2
	modeCol := modeScreenStart - popoverLeft - contentBorder

	aliasW := arrowCol - aliasCol - gap
	canonW := modeCol - canonCol - gap
	if aliasW < 4 {
		aliasW = 4
	}
	if canonW < 4 {
		canonW = 4
	}

	rows := p.rows
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
	}

	// When alias equals canonical (the typical case — no cross-language
	// resolution needed), collapse the row to a single name spanning the
	// alias + arrow + canonical area. The arrow + duplicated canonical
	// only appear when they carry information.
	wideW := modeCol - aliasCol - gap
	if wideW < 4 {
		wideW = 4
	}

	// queryFolded is used to decide whether the alias adds information.
	// If the typed query is already a (diacritic-folded) substring of the
	// canonical, the alias would just repeat what the row already shows —
	// collapse to single column. Two-column form is reserved for cases
	// where the alias genuinely surfaces something new (cross-language,
	// the API-resolved address case, etc.).
	queryFolded := stations.FoldQuery(m.inputs[p.inputIdx].Value())

	var lines []string
	for i, r := range rows {
		mm := r.match
		canonFolded := stations.FoldQuery(mm.Station.Name)
		sameAsCanon := mm.AliasText == mm.Station.Name ||
			(queryFolded != "" && strings.Contains(canonFolded, queryFolded))

		// Remote rows get the ⌕ glyph baked into the mode badge to mark
		// them as API-sourced; local rows render mode as-is. Long modes
		// (CABLE_RAILWAY, RACK_RAILWAY) are truncated with the same
		// "…" treatment used elsewhere instead of a hand-curated
		// display map — keeps the canonical mode string in storage so
		// ScoreConfig.ModeBonus lookups don't need a parallel mapping.
		modeLabel := mm.Station.Mode
		if r.source != sourceLocal {
			modeLabel = m.icons.search + " " + mm.Station.Mode
		}
		modeLabel, _ = truncateRunes(modeLabel, modeBadgeW, nil)
		modePart := lipgloss.PlaceHorizontal(modeBadgeW, lipgloss.Left, modeStyle.Render(modeLabel))

		var b strings.Builder
		if i == p.selected {
			b.WriteString(" " + markerStyle.Render("▶") + " ")
		} else {
			b.WriteString("   ")
		}
		col := 3
		if aliasCol > col {
			b.WriteString(strings.Repeat(" ", aliasCol-col))
			col = aliasCol
		}

		if sameAsCanon {
			// One column: the canonical name, with matched chars highlighted,
			// occupying everything up to the mode badge.
			text, matchedAdj := truncateRunes(mm.Station.Name, wideW, mm.MatchedRunes)
			rendered := renderHighlightedAlias(text, matchedAdj, highlightStyle, canonStyle)
			b.WriteString(lipgloss.PlaceHorizontal(wideW, lipgloss.Left, rendered))
			col += wideW
		} else {
			// Two columns + arrow — the cross-language / address-resolution
			// case where the alias and canonical legitimately differ.
			aliasText, matchedAdj := truncateRunes(mm.AliasText, aliasW, mm.MatchedRunes)
			canonText, _ := truncateRunes(mm.Station.Name, canonW, nil)

			alias := renderHighlightedAlias(aliasText, matchedAdj, highlightStyle, rowStyle)
			b.WriteString(lipgloss.PlaceHorizontal(aliasW, lipgloss.Left, alias))
			col += aliasW
			if arrowCol > col {
				b.WriteString(strings.Repeat(" ", arrowCol-col))
				col = arrowCol
			}
			b.WriteString(modeStyle.Render("→"))
			col++
			if canonCol > col {
				b.WriteString(strings.Repeat(" ", canonCol-col))
				col = canonCol
			}
			b.WriteString(lipgloss.PlaceHorizontal(canonW, lipgloss.Left, canonStyle.Render(canonText)))
			col += canonW
		}

		if modeCol > col {
			b.WriteString(strings.Repeat(" ", modeCol-col))
			col = modeCol
		}
		b.WriteString(modePart)

		lines = append(lines, b.String())
	}

	// Sentinel row — always rendered, sits at the virtual position
	// len(rows) so the user can arrow down past every match and hit Enter
	// to submit their literal typed text (bypassing fuzzy resolution).
	// Solves both the "no match" dead-end and the "wrong match overrode my
	// input" footguns.
	sentinelSelected := p.selected >= len(rows)
	lines = append(lines, m.renderPopoverSentinel(p, sentinelSelected, markerStyle))

	block := lipgloss.JoinVertical(lipgloss.Left, lines...)
	return indentLines(box.Render(block), popoverLeft)
}

// renderPopoverSentinel produces the always-visible last row of the
// popover, which surfaces the /v1/locations back-fill status AND acts
// as a selectable "submit as typed" affordance.
func (m appModel) renderPopoverSentinel(p *popoverState, selected bool, markerStyle lipgloss.Style) string {
	icon := m.icons.search
	muted := m.styles.textMuted
	q := p.query

	var label string
	switch p.remoteStatus {
	case remoteLoading:
		label = icon + " searching remotely for \"" + q + "\"…"
	case remoteError:
		label = icon + " remote search failed — retry on next keystroke"
	default:
		label = icon + " search remotely for \"" + q + "\""
	}

	var prefix string
	if selected {
		prefix = " " + markerStyle.Render("▶") + " "
		return prefix + m.styles.text.Render(label)
	}
	prefix = "   "
	return prefix + muted.Render(label)
}

// indentLines prepends pad spaces to every line of s.
func indentLines(s string, pad int) string {
	if pad <= 0 {
		return s
	}
	leading := strings.Repeat(" ", pad)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = leading + l
	}
	return strings.Join(lines, "\n")
}

// truncateRunes returns s truncated to at most max display columns, with a
// trailing "…" when truncation occurred. The matched-rune positions are
// adjusted to stay valid against the returned string (dropped if past the cut).
func truncateRunes(s string, max int, matched []int) (string, []int) {
	if lipgloss.Width(s) <= max {
		return s, matched
	}
	runes := []rune(s)
	out := make([]rune, 0, max)
	width := 0
	for i, r := range runes {
		w := lipgloss.Width(string(r))
		if width+w+1 > max { // +1 to reserve room for the ellipsis
			_ = i
			break
		}
		out = append(out, r)
		width += w
	}
	out = append(out, '…')

	if matched == nil {
		return string(out), nil
	}
	cutoff := len(out) - 1
	keep := matched[:0]
	for _, p := range matched {
		if p < cutoff {
			keep = append(keep, p)
		}
	}
	return string(out), keep
}

// renderHighlightedAlias renders s with matched runes wrapped in highlight
// and the rest in base — matched positions are 0-indexed rune offsets.
func renderHighlightedAlias(s string, matched []int, highlight, base lipgloss.Style) string {
	if len(matched) == 0 {
		return base.Render(s)
	}
	hit := make(map[int]bool, len(matched))
	for _, p := range matched {
		hit[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(s) {
		if hit[i] {
			b.WriteString(highlight.Render(string(r)))
		} else {
			b.WriteString(base.Render(string(r)))
		}
	}
	return b.String()
}

// renderDetailedResult renders the right-hand detail box for the currently selected connection.
func (m appModel) renderDetailedResult() string {
	if len(m.connections) == 0 {
		return ""
	}

	boxWidth := max(m.width-borderSize*2-m.resultBoxWidth(), 0)
	return m.renderFullConnection(m.connections[m.resultIndex], boxWidth)
}
