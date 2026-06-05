package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/text/unicode/norm"

	"github.com/necrom4/sbb-tui/api"
	"github.com/necrom4/sbb-tui/model"
	"github.com/necrom4/sbb-tui/ui/stations"
)

// Update implements tea.Model.
func (m appModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		remaining := m.width - m.headerFixedWidth()
		inputWidth := max(remaining/2, 1)
		m.inputs[0].Width = inputWidth
		m.inputs[1].Width = inputWidth

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "esc":
			// First press cancels any modal affordance — an open popover
			// and/or the overwrite-on-refocus state; second press quits.
			canceled := false
			if active := m.headerOrder[m.tabIndex]; active.kind == kindInput && (active.index == 0 || active.index == 1) {
				if m.overwriteOnType[active.index] {
					m.setOverwrite(active.index, false)
					canceled = true
				}
			}
			if m.popover != nil {
				m.popover = nil
				canceled = true
			}
			if canceled {
				return m, nil
			}
			return m, tea.Quit

		case "q":
			active := m.headerOrder[m.tabIndex]
			if active.kind == kindButton {
				return m, tea.Quit
			}

		case "enter":
			// If the focused input is in overwrite (refocused-with-content)
			// state, the user is committing the prior value — do not swap
			// it for the popover's top match. Otherwise commit popover
			// selection so a freshly-typed alias resolves to canonical.
			active := m.headerOrder[m.tabIndex]
			overwriteActive := false
			if active.kind == kindInput && (active.index == 0 || active.index == 1) {
				overwriteActive = m.overwriteOnType[active.index]
			}
			if overwriteActive {
				m.setOverwrite(active.index, false)
			} else {
				m.commitPopoverSelection()
			}
			// Always close the popover on search — including the "no match"
			// case where the typed input is something the API can still
			// resolve (e.g. an address like "11 route de bardonnex").
			m.popover = nil
			if err := m.validateInputs(); err != nil {
				m.errorMsg = err
				m.connections = nil
				m.searched = false
				m.resultIndex = 0
				return m, nil
			}
			m.loading = true
			m.connections = nil
			m.errorMsg = nil
			m.searched = true
			return m, tea.Batch(m.searchCmd(), m.startLoadingCmd())

		case " ":
			active := m.headerOrder[m.tabIndex]
			switch active.id {
			case "swap":
				tmp := m.inputs[0].Value()
				m.inputs[0].SetValue(m.inputs[1].Value())
				m.inputs[1].SetValue(tmp)
			case "isArrivalTime":
				m.isArrivalTime = !m.isArrivalTime
			case "search":
				if err := m.validateInputs(); err != nil {
					m.errorMsg = err
					m.connections = nil
					m.searched = false
					m.resultIndex = 0
					return m, nil
				}
				m.loading = true
				m.connections = nil
				m.errorMsg = nil
				m.searched = true
				return m, tea.Batch(m.searchCmd(), m.startLoadingCmd())
			}

		case "tab", "shift+tab":
			// Clear overwrite affordance on the old field (if any) before
			// leaving so the value renders normally next time.
			if oldActive := m.headerOrder[m.tabIndex]; oldActive.kind == kindInput && (oldActive.index == 0 || oldActive.index == 1) {
				if m.overwriteOnType[oldActive.index] {
					m.setOverwrite(oldActive.index, false)
				}
			}

			// Commit popover before leaving the field so the next field
			// receives the canonical resolution of what was typed.
			m.commitPopoverSelection()

			if msg.String() == "shift+tab" {
				m.tabIndex--
			} else {
				m.tabIndex++
			}

			if m.tabIndex >= len(m.headerOrder) {
				m.tabIndex = 0
			}
			if m.tabIndex < 0 {
				m.tabIndex = len(m.headerOrder) - 1
			}

			// Move focus to the newly active input and blur every other one.
			var cmds []tea.Cmd
			for _, item := range m.headerOrder {
				if item.kind == kindInput {
					if item.index == m.headerOrder[m.tabIndex].index {
						cmds = append(cmds, m.inputs[item.index].Focus())
					} else {
						m.inputs[item.index].Blur()
					}
				}
			}

			// Repopulate the popover for the newly focused From/To field
			// and, if it already has content, switch on the overwrite
			// affordance: value renders faded, cursor at start. Typing a
			// character replaces the value; Enter keeps it as-is.
			active := m.headerOrder[m.tabIndex]
			if m.fuzzy && active.kind == kindInput && (active.index == 0 || active.index == 1) {
				if m.inputs[active.index].Value() != "" {
					m.setOverwrite(active.index, true)
				}
				m.refreshPopover(active.index)
			} else {
				m.popover = nil
			}
			return m, tea.Batch(cmds...)

		case "right":
			// In overwrite (refocus-with-content) state, → cancels the
			// affordance, parks the cursor at end-of-value, and lets the
			// user keep typing from there. Acts as "append to prior value".
			if active := m.headerOrder[m.tabIndex]; active.kind == kindInput && (active.index == 0 || active.index == 1) && m.overwriteOnType[active.index] {
				m.inputs[active.index].SetCursor(len([]rune(m.inputs[active.index].Value())))
				m.setOverwrite(active.index, false)
				return m, nil
			}
			// Suppress autocomplete acceptance when the cursor is mid-string;
			// the user just wants to move right.
			active := m.headerOrder[m.tabIndex]
			if active.kind == kindInput {
				input := m.inputs[active.index]
				if input.Position() < len([]rune(input.Value())) {
					original := input.KeyMap.AcceptSuggestion
					input.KeyMap.AcceptSuggestion = key.NewBinding()

					var cmd tea.Cmd
					m.inputs[active.index], cmd = input.Update(msg)
					m.inputs[active.index].KeyMap.AcceptSuggestion = original

					return m, cmd
				}
			}

		case "up":
			if m.popover != nil {
				// Selection wraps through rows + the trailing sentinel,
				// so a deliberate up-from-row-0 lands on "search remotely
				// for '<query>'" which submits the literal typed text.
				last := len(m.popover.rows)
				if m.popover.selected > 0 {
					m.popover.selected--
				} else {
					m.popover.selected = last
				}
				return m, nil
			}
			if len(m.connections) > 0 && m.resultIndex > 0 {
				m.resultIndex--
				m.detailScrollY = 0
			}
		case "down":
			if m.popover != nil {
				last := len(m.popover.rows)
				if m.popover.selected < last {
					m.popover.selected++
				} else {
					m.popover.selected = 0
				}
				return m, nil
			}
			if len(m.connections) > 0 && m.resultIndex < len(m.connections)-1 {
				m.resultIndex++
				m.detailScrollY = 0
			}
		case "shift+up":
			if m.detailScrollY > 0 {
				m.detailScrollY--
			}
		case "shift+down":
			if max := m.maxDetailScroll(); m.detailScrollY < max {
				m.detailScrollY++
			}
		}

	case suggestTickMsg:
		// Only fetch when no newer keystroke has invalidated this tick.
		if msg.seq != m.suggestSeq[msg.inputIndex] {
			return m, nil
		}
		return m, fetchSuggestionsCmd(msg.inputIndex, m.inputs[msg.inputIndex].Value())

	case suggestionsMsg:
		if msg.err == nil {
			userInput := m.inputs[msg.inputIndex].Value()
			m.inputs[msg.inputIndex].SetSuggestions(adaptSuggestions(userInput, msg.names))
		}
		return m, nil

	case remoteSuggestTickMsg:
		if msg.seq != m.remoteSuggestSeq[msg.inputIndex] {
			return m, nil
		}
		return m, fetchRemoteLocationsCmd(msg.inputIndex, msg.seq, m.inputs[msg.inputIndex].Value())

	case remoteLocationsMsg:
		if msg.seq != m.remoteSuggestSeq[msg.inputIndex] {
			return m, nil
		}
		if msg.err != nil {
			if m.popover != nil && m.popover.inputIdx == msg.inputIndex && m.popover.query == msg.query {
				m.popover.remoteStatus = remoteError
			}
			return m, nil
		}
		m.mergeRemoteLocations(msg.inputIndex, msg.query, msg.locations)
		return m, nil

	case dataMsg:
		m.loading = false
		m.anim.Stop(animLoading)
		if msg.err != nil {
			m.errorMsg = fmt.Errorf("failed to fetch connections: %w", msg.err)
			return m, nil
		}
		m.connections = msg.connections
		m.resultIndex = 0
		m.detailScrollY = 0
		if len(m.connections) == 0 {
			m.errorMsg = errNoConnections
		}
		return m, nil

	case versionCheckMsg:
		m.newerVersion = msg.newerVersion
		return m, nil

	case animationTickMsg:
		finished, next := m.anim.Tick()
		cmds := []tea.Cmd{next}
		if m.animations && m.onStartScreen() {
			cmds = append(cmds, m.onAnimationsFinished(finished)...)
		}
		return m, tea.Batch(cmds...)

	case shineRestartMsg:
		if !m.animations || !m.onStartScreen() {
			return m, nil
		}
		return m, m.startShineCycle()
	}

	cmd := m.updateInputs(msg)
	return m, cmd
}

// updateInputs forwards keypresses to the text inputs and triggers the
// debounced suggestion fetches and ghost-completion updates.
func (m *appModel) updateInputs(msg tea.Msg) tea.Cmd {
	cmds := make([]tea.Cmd, len(m.inputs))

	// Overwrite-on-refocus: when the focused From/To input is showing a
	// faded prior value, the first typed rune wipes the value before the
	// textinput sees the keystroke. Backspace cancels the affordance and
	// moves the cursor to the end, so the next Backspace deletes a char
	// normally instead of being a no-op at position 0.
	if keyMsg, ok := msg.(tea.KeyMsg); ok {
		active := m.headerOrder[m.tabIndex]
		if active.kind == kindInput && (active.index == 0 || active.index == 1) && m.overwriteOnType[active.index] {
			switch keyMsg.Type {
			case tea.KeyRunes:
				m.inputs[active.index].SetValue("")
				m.inputs[active.index].SetCursor(0)
				if active.index == 0 {
					m.lastFromQuery = ""
				} else {
					m.lastToQuery = ""
				}
				m.setOverwrite(active.index, false)
			case tea.KeyBackspace:
				m.inputs[active.index].SetCursor(len([]rune(m.inputs[active.index].Value())))
				m.setOverwrite(active.index, false)
			}
		}
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Date and time inputs handle their own digit-only logic so the
		// dot/colon delimiters stay locked in place no matter where the
		// user moves the cursor.
		switch m.headerOrder[m.tabIndex].id {
		case "date":
			t := &m.inputs[2]
			s := msg.String()
			val := t.Value()

			digits := stripDelimiters(val, '.')

			if msg.Type == tea.KeyBackspace {
				pos := t.Position()
				digitPos := countDigitsBefore(val, pos)
				if digitPos > 0 && digitPos <= len(digits) {
					digits = digits[:digitPos-1] + digits[digitPos:]
					formatted := formatDate(digits)
					t.SetValue(formatted)
					newPos := posOfDigit(formatted, digitPos-1)
					t.SetCursor(newPos)
				}
				return nil
			}

			if len(s) == 1 && s >= "0" && s <= "9" {
				if len(digits) >= 8 {
					return nil
				}
				pos := t.Position()
				digitPos := countDigitsBefore(val, pos)
				newDigits := digits[:digitPos] + s + digits[digitPos:]

				if !validateDateDigits(newDigits) {
					return nil
				}

				formatted := formatDate(newDigits)
				t.SetValue(formatted)
				t.SetCursor(posOfDigit(formatted, digitPos+1))
				return nil
			} else if msg.Type == tea.KeyRunes {
				return nil
			}

		case "time":
			t := &m.inputs[3]
			s := msg.String()
			val := t.Value()

			digits := stripDelimiters(val, ':')

			if msg.Type == tea.KeyBackspace {
				pos := t.Position()
				digitPos := countDigitsBefore(val, pos)
				if digitPos > 0 && digitPos <= len(digits) {
					digits = digits[:digitPos-1] + digits[digitPos:]
					formatted := formatTime(digits)
					t.SetValue(formatted)
					newPos := posOfDigit(formatted, digitPos-1)
					t.SetCursor(newPos)
				}
				return nil
			}

			if len(s) == 1 && s >= "0" && s <= "9" {
				if len(digits) >= 4 {
					return nil
				}
				pos := t.Position()
				digitPos := countDigitsBefore(val, pos)
				newDigits := digits[:digitPos] + s + digits[digitPos:]

				if !validateTimeDigits(newDigits) {
					return nil
				}

				formatted := formatTime(newDigits)
				t.SetValue(formatted)
				t.SetCursor(posOfDigit(formatted, digitPos+1))
				return nil
			} else if msg.Type == tea.KeyRunes {
				return nil
			}
		}
	}

	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}

	// Refresh suggestions for the From/To values. With fuzzy enabled the
	// popover replaces the ghost-completion suggestion entirely and is
	// computed synchronously from the local index. The async suggestion
	// fetch only fires for the --fuzzy=false legacy path.
	for inputIdx := 0; inputIdx < 2; inputIdx++ {
		val := m.inputs[inputIdx].Value()
		var prev *string
		if inputIdx == 0 {
			prev = &m.lastFromQuery
		} else {
			prev = &m.lastToQuery
		}
		if val == *prev {
			continue
		}
		*prev = val
		if m.fuzzy {
			m.refreshPopover(inputIdx)
			if len(val) >= 2 {
				m.remoteSuggestSeq[inputIdx]++
				seq := m.remoteSuggestSeq[inputIdx]
				idx := inputIdx
				cmds = append(cmds, tea.Tick(suggestDebounce, func(time.Time) tea.Msg {
					return remoteSuggestTickMsg{inputIndex: idx, seq: seq}
				}))
			}
		} else if len(val) >= 2 {
			m.suggestSeq[inputIdx]++
			seq := m.suggestSeq[inputIdx]
			idx := inputIdx
			cmds = append(cmds, tea.Tick(suggestDebounce, func(time.Time) tea.Msg {
				return suggestTickMsg{inputIndex: idx, seq: seq}
			}))
		} else {
			m.inputs[inputIdx].SetSuggestions(nil)
		}
	}

	// Refresh the ghost-completion offered by the date/time inputs.
	m.inputs[2].SetSuggestions([]string{completeDate(m.inputs[2].Value())})
	m.inputs[3].SetSuggestions([]string{completeTime(m.inputs[3].Value())})

	return tea.Batch(cmds...)
}

// validateInputs returns an error when the From or To station is blank.
func (m appModel) validateInputs() error {
	if m.inputs[0].Value() == "" {
		return errMissingDeparture
	}
	if m.inputs[1].Value() == "" {
		return errMissingArrival
	}
	return nil
}

// refreshPopover runs the local fuzzy matcher against the current value of
// inputs[inputIdx] and populates m.popover with the local rows. The async
// /v1/locations back-fill is kicked off separately by updateInputs so the
// remote rows can land later as a remoteLocationsMsg. Nil-ed when the input
// is too short or the matcher is unavailable.
func (m *appModel) refreshPopover(inputIdx int) {
	if m.fuzzyIdx == nil {
		m.popover = nil
		return
	}
	active := m.headerOrder[m.tabIndex]
	if active.kind != kindInput || active.index != inputIdx {
		if m.popover != nil && m.popover.inputIdx == inputIdx {
			m.popover = nil
		}
		return
	}
	val := m.inputs[inputIdx].Value()
	if len(val) < 2 {
		m.popover = nil
		return
	}
	matches := m.fuzzyIdx.Search(val, popoverRows, m.scoreCfg)
	rows := make([]popoverRow, 0, len(matches))
	for _, mm := range matches {
		rows = append(rows, popoverRow{match: mm, source: sourceLocal})
	}
	m.popover = &popoverState{
		inputIdx:     inputIdx,
		rows:         rows,
		selected:     0,
		query:        val,
		remoteStatus: remoteLoading,
	}
}

// mergeRemoteLocations folds /v1/locations results into the current
// popover, deduping by UIC (local rows win — they carry mode + aliases
// + matched-rune positions the API can't give us). Address and POI rows
// — which have no UIC — always pass through.
func (m *appModel) mergeRemoteLocations(inputIdx int, query string, locs []api.Location) {
	p := m.popover
	if p == nil || p.inputIdx != inputIdx || p.query != query {
		return
	}
	existing := make(map[string]bool, len(p.rows))
	for _, r := range p.rows {
		if r.source == sourceLocal && r.match.Station.UIC != "" {
			existing[r.match.Station.UIC] = true
		}
	}
	for _, loc := range locs {
		src := sourceRemoteStation
		mode := ""
		switch loc.Type {
		case "address":
			src = sourceRemoteAddress
			mode = "ADDRESS"
		case "poi":
			src = sourceRemotePOI
			mode = "POI"
		default:
			mode = "STATION"
		}
		if loc.ID != "" && existing[loc.ID] {
			continue
		}
		p.rows = append(p.rows, popoverRow{
			match: stations.Match{
				Station: stations.Station{
					UIC:  loc.ID,
					Name: loc.Name,
					Mode: mode,
				},
				AliasText:    loc.Name,
				MatchedRunes: stations.PositionsFor(query, loc.Name),
			},
			source: src,
		})
	}
	p.remoteStatus = remoteDone
}

// popoverRows is the maximum number of rows shown in the fuzzy popover.
const popoverRows = 8

// commitPopoverSelection replaces the focused input's value with the canonical
// name of the highlighted popover row and clears the popover. No-op when the
// popover is hidden, empty, or when the sentinel row is selected (the user
// explicitly chose "search remotely for the literal typed text").
func (m *appModel) commitPopoverSelection() {
	p := m.popover
	if p == nil || len(p.rows) == 0 {
		return
	}
	// Sentinel row is the virtual position len(rows). Selecting it means
	// "leave my input alone and submit it as typed".
	if p.selected >= len(p.rows) {
		return
	}
	canonical := p.rows[p.selected].match.Station.Name
	m.inputs[p.inputIdx].SetValue(canonical)
	m.inputs[p.inputIdx].SetCursor(len([]rune(canonical)))
	if p.inputIdx == 0 {
		m.lastFromQuery = canonical
	} else {
		m.lastToQuery = canonical
	}
	m.popover = nil
}

// fetchSuggestionsCmd asynchronously asks the API for station suggestions.
func fetchSuggestionsCmd(inputIndex int, query string) tea.Cmd {
	return func() tea.Msg {
		names, err := api.FetchLocations(query)
		return suggestionsMsg{inputIndex: inputIndex, names: names, err: err}
	}
}

// fetchRemoteLocationsCmd backs the fuzzy popover's API-merge row by
// asking /v1/locations for full Location rows. seq stamps the response
// so out-of-order replies are dropped at message-handle time.
func fetchRemoteLocationsCmd(inputIndex, seq int, query string) tea.Cmd {
	return func() tea.Msg {
		locs, err := api.SearchLocations(query)
		return remoteLocationsMsg{
			inputIndex: inputIndex,
			seq:        seq,
			query:      query,
			locations:  locs,
			err:        err,
		}
	}
}

// completeDate returns partial padded out to today's date in DD.MM.YYYY form.
func completeDate(partial string) string {
	now := time.Now().In(model.SwissLocation)
	full := now.Format("02.01.2006")
	if len(partial) < len(full) {
		return partial + full[len(partial):]
	}
	return partial
}

// completeTime returns partial padded out to "00:00" form.
func completeTime(partial string) string {
	if len(partial) < 5 {
		full := partial + "00:00"[len(partial):]
		return full
	}
	return partial
}

// stripDelimiters removes every occurrence of delim from s.
func stripDelimiters(s string, delim byte) string {
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != delim {
			result = append(result, s[i])
		}
	}
	return string(result)
}

// countDigitsBefore returns the number of non-delimiter bytes before pos in s.
func countDigitsBefore(s string, pos int) int {
	count := 0
	for i := 0; i < pos && i < len(s); i++ {
		if s[i] != '.' && s[i] != ':' {
			count++
		}
	}
	return count
}

// posOfDigit returns the byte index of the n-th digit (0-indexed) in s,
// or len(s) when n exceeds the digit count.
func posOfDigit(s string, n int) int {
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] != '.' && s[i] != ':' {
			if count == n {
				return i
			}
			count++
		}
	}
	return len(s)
}

// formatDate inserts dots into a digit string: DDMMYYYY -> DD.MM.YYYY.
func formatDate(digits string) string {
	var b strings.Builder
	b.Grow(len(digits) + 2)
	for i, c := range digits {
		if i == 2 || i == 4 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// formatTime inserts a colon into a digit string: HHMM -> HH:MM.
func formatTime(digits string) string {
	var b strings.Builder
	b.Grow(len(digits) + 1)
	for i, c := range digits {
		if i == 2 {
			b.WriteByte(':')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// validateDateDigits rejects partial date digits that can never form a valid date.
func validateDateDigits(d string) bool {
	if len(d) >= 1 && d[0] > '3' {
		return false
	}
	if len(d) >= 2 {
		if d[0] == '0' && d[1] == '0' {
			return false
		}
		if d[0] == '3' && d[1] > '1' {
			return false
		}
	}
	if len(d) >= 3 && d[2] > '1' {
		return false
	}
	if len(d) >= 4 {
		if d[2] == '0' && d[3] == '0' {
			return false
		}
		if d[2] == '1' && d[3] > '2' {
			return false
		}
	}
	if len(d) >= 5 && d[4] > '2' {
		return false
	}
	return true
}

// validateTimeDigits rejects partial time digits that can never form a valid time.
func validateTimeDigits(d string) bool {
	if len(d) >= 1 && d[0] > '2' {
		return false
	}
	if len(d) >= 2 && d[0] == '2' && d[1] > '3' {
		return false
	}
	if len(d) >= 3 && d[2] > '5' {
		return false
	}
	return true
}

// toAPIDate converts the Swiss DD.MM.YYYY format to the API's YYYY-MM-DD.
func toAPIDate(swiss string) string {
	parts := strings.Split(swiss, ".")
	if len(parts) != 3 {
		return swiss
	}
	return parts[2] + "-" + parts[1] + "-" + parts[0]
}

// searchCmd asynchronously runs the connections search with the current input values.
func (m appModel) searchCmd() tea.Cmd {
	return func() tea.Msg {
		res, err := api.FetchConnections(
			m.inputs[0].Value(),
			m.inputs[1].Value(),
			toAPIDate(completeDate(m.inputs[2].Value())),
			completeTime(m.inputs[3].Value()),
			m.isArrivalTime,
			m.maxVisibleConnections(),
		)
		return dataMsg{connections: res, err: err}
	}
}

// adaptSuggestions grafts the user's literal input onto the front of each
// suggestion so the textinput widget's HasPrefix matching accepts them.
// (e.g. "zur" + "Zürich HB" -> "zurich HB")
func adaptSuggestions(userInput string, suggestions []string) []string {
	if userInput == "" {
		return suggestions
	}
	lower := strings.ToLower(userInput)
	out := make([]string, 0, len(suggestions))
	for _, s := range suggestions {
		idx := prefixMatchLen(strings.ToLower(s), lower)
		if idx > 0 {
			out = append(out, userInput+s[idx:])
		}
	}
	return out
}

// prefixMatchLen returns the byte offset into suggestion that is matched by
// input under fuzzy rules (diacritic folding + skipping non-alphanumerics).
func prefixMatchLen(suggestion, input string) int {
	si, ii := 0, 0
	for si < len(suggestion) && ii < len(input) {
		sr, sw := utf8.DecodeRuneInString(suggestion[si:])
		ir, iw := utf8.DecodeRuneInString(input[ii:])

		if sr == ir {
			si += sw
			ii += iw
			continue
		}

		// Skip punctuation/spaces in the suggestion without consuming input.
		if !unicode.IsLetter(sr) && !unicode.IsDigit(sr) {
			si += sw
			continue
		}

		if foldRune(sr) == foldRune(ir) {
			si += sw
			ii += iw
			continue
		}

		return 0
	}

	if ii < len(input) {
		// Suggestion ran out before input was consumed.
		return 0
	}
	return si
}

// foldRune returns the base rune of r after NFD decomposition,
// stripping common diacritics (e.g. ü -> u).
func foldRune(r rune) rune {
	decomposed := norm.NFD.String(string(r))
	base, _ := utf8.DecodeRuneInString(decomposed)
	return base
}
