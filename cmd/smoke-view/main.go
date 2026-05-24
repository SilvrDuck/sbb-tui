// Binary smoke-view renders the appModel.View() once and reports line counts.
package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/necrom4/sbb-tui/config"
	"github.com/necrom4/sbb-tui/ui"
)

func main() {
	cfg, _ := config.LoadConfig()
	cfg.Fuzzy = true
	cfg.Animations = false
	cfg.NerdFont = true
	cfg.CurrentVersion = "dev"

	var m tea.Model = ui.NewModel(cfg)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	for _, r := range "gen" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	out := m.(interface{ View() string }).View()
	fmt.Printf("=== View() rendered: %d lines, width measured: %d ===\n",
		strings.Count(out, "\n")+1, lipgloss.Width(out))
	fmt.Println(out)
}
