package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var docStyle = lipgloss.NewStyle().Margin(1, 2)

// version is overwritten at build time via -ldflags by GoReleaser.
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v", "version":
			fmt.Println("pedantic", version)
			return
		}
	}

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

type model struct {
	textarea textarea.Model
	results  *PromQLResults
	err      error
	width    int
}

func initialModel() model {
	ti := textarea.New()
	ti.Placeholder = "up{host=\"foo\"}"
	ti.ShowLineNumbers = true
	ti.DynamicHeight = true
	ti.MinHeight = 3
	ti.MaxHeight = 15
	ti.MaxContentHeight = 20
	ti.SetWidth(1000)
	ti.SetVirtualCursor(false)
	ti.Focus()

	return model{textarea: ti}
}

type queryResultMsg struct {
	results *PromQLResults
	err     error
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tea.RequestBackgroundColor)
}

func runQueryCommand(query string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		results, err := RunPromQl(ctx, query)
		return queryResultMsg{results: results, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.BackgroundColorMsg:
		m.textarea.SetStyles(textarea.DefaultStyles(msg.IsDark()))
	case queryResultMsg:
		// Check the error before touching results — on the error path
		// results is nil.
		if msg.err != nil {
			m.err = msg.err
			m.results = nil
		} else {
			m.err = nil
			m.results = msg.results
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+enter":
			return m, runQueryCommand(m.textarea.Value())
		case "ctrl+backspace":
			m.textarea.SetValue("")
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) View() tea.View {
	const gap = 1

	var c *tea.Cursor
	if !m.textarea.VirtualCursor() {
		c = m.textarea.Cursor()
		c.Y += gap
	}

	sections := []string{
		m.textarea.View(),
	}
	if m.err != nil {
		sections = append(sections, m.errorView())
	} else if res := m.resultsView(); res != "" {
		sections = append(sections, res)
	}
	sections = append(sections, "\n(ctrl+enter to run · ctrl+c to quit)")

	f := strings.Repeat("\n", gap) + strings.Join(sections, "\n")

	v := tea.NewView(f)
	v.Cursor = c
	return v
}
