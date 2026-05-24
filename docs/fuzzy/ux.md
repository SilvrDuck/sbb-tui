# UX Behaviour

The picker's user-facing affordances — keymap, popover anchoring,
collapse rules, refocus-overwrite.

## Popover lifecycle

The popover appears when:

- A From or To input is focused **and**
- The input's value length ≥ 2 characters **and**
- `--fuzzy` is enabled (default true)

It disappears when:

- The value drops below 2 characters
- Tab moves focus to a button (swap, arrival, search)
- `Esc` is pressed (and overwrite-mode is not active)
- `Enter` runs a search

## Keymap (popover open)

| Key | Behaviour |
|---|---|
| Typing a character | Edits input, refilters popover, resets selection to row 0 |
| `Backspace` | Edits input |
| `↑` / `↓` | Move selection within popover (wraps around) |
| `Tab` / `Shift+Tab` | Commit highlighted row's canonical into the input, close popover, advance to next/prev field |
| `Enter` | Commit highlighted row's canonical, close popover, run search if both From and To are valid |
| `Esc` | Close popover (first press); quit (second press, or first press if popover already closed) |
| `Ctrl+C` | Always quit |
| `q` | Insert literal `q` into input (only quits when focus is on a button) |

The fast UX is **type → top row is the implicit selection → Tab/Enter
commits it**. You never need the arrow keys unless you want to pick a
non-top row.

## Refocus-overwrite affordance

When you Tab back into a From/To field that already has content, the
field switches into a "type to rewrite, Enter to keep" mode:

- The existing value renders in the muted text style (faded).
- The cursor parks at column 0 of the value.
- The help bar swaps to overwrite-specific bindings:

  ```
  ↵ keep    → append    abc rewrite    ⇥ next    ⎋ cancel
  ```

| Key (overwrite-mode) | Behaviour |
|---|---|
| Any printable character | Clear the value, then insert this character as the first char |
| `→` | Cancel overwrite, park cursor at end of value, continue editing from there |
| `Backspace` | Cancel overwrite, park cursor at end of value, then delete one char |
| `Enter` | Use the prior value as-is (do NOT commit the popover's top match) |
| `Esc` | Cancel overwrite and close popover; second Esc quits |
| `Tab` / `Shift+Tab` | Move to next/prev field; popover commit may still apply if it was open |

The affordance is the standard browser-URL-bar pattern: select-on-focus,
type-to-replace, arrow-to-edit-in-place.

## Popover positioning

Anchored to the focused input's left border:

```
focused on From  →  popover starts at column 0 (full width minus content tail)
focused on To    →  popover starts at the To-input's left column
```

The popover spans only as wide as its content needs (alias column +
arrow + canonical column + mode badge + borders), clamped never to
exceed the screen width. The arrow position is computed to land at
exactly the column where the *next* header field begins — for `From`
focused this is the `To` input's left edge.

## Column layout inside the popover

Same row structure mirrors the input row above:

```
[marker] [alias column ↦ aligned with From value] [arrow ↦ at To-box edge] [canonical column ↦ aligned with To value] [mode badge]
```

Per row, two layouts:

### Single-column (alias adds no info)

When the user's typed query is a diacritic-folded substring of the
canonical name, the alias would just repeat what the canonical shows.
We collapse to:

```
▶ Renens VD                                                        TRAIN
  FLAVIAC René Cassin                                              BUS
  Renens VD, gare                                                  BUS
```

### Two-column (alias adds info)

When the query is NOT a substring of the canonical (cross-language,
typo, API-resolved address), we render alias + arrow + canonical:

```
▶ Genf                   →  Genève                                 TRAIN
  Genf-Sécheron          →  Genève-Sécheron                        TRAIN
  Ginevra                →  Genève                                 TRAIN
  Cornavin               →  Genève                                 TRAIN
```

The matched characters in the alias column are styled in the focus
accent colour; the canonical is rendered bold.

## Z-index over the start screen

The popover paints **on top of** the start-screen content (SBB logo +
tagline) instead of pushing it down. Implementation:

1. Render the start screen at full `resultsHeight()`.
2. Render the popover into its own block.
3. Splice each popover line over the corresponding base line using
   `ansi.Cut`, preserving both blocks' ANSI styles.

Lines below the popover show the rest of the logo / tagline visible.

When a real search has populated `connections`, the same overlay
mechanism splices the popover over the connection list — but the
popover compacts to 4 rows so at least one full connection card stays
visible underneath.

## Search-icon truncation fix

`textinput.View()` always emits `promptW + Width + 1` columns — the
trailing `+1` is the cursor cell, regardless of focus. Mainline
truncated only when `ShowSuggestions == true`, which was unconditionally
true there. The fuzzy popover replaces ghost completion (sets
`ShowSuggestions = false` on From/To), so the truncation was skipped and
each input grew by 1 column. Two inputs × 1 col = 2 columns of header
overflow, pushing the search ⌕ button off the right edge.

Fix: `ansi.Truncate(view, promptW+Width, "")` unconditional in
`renderHeaderItem`.

## Theming

Aliases highlight, popover border, marker, and help-bar bindings all
draw from existing `Theme` fields — primarily `BorderFocused` (focus
accent) and `Text` / `TextMuted` (canonical + muted). No new theme
fields were added during the spike, though a future iteration could add
`MatchHighlight` if a separate accent is wanted.
