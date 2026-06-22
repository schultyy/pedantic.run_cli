package main

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// Verdicts ordered most→least expensive. Drives the summary order, the sort of
// the findings list, and which verdict "wins" when highlighted spans overlap.
var verdictOrder = []string{"invalid", "slow", "moderate", "runtime_dependent", "fast"}

var verdictSeverity = map[string]int{
	"invalid":           5,
	"slow":              4,
	"moderate":          3,
	"runtime_dependent": 2,
	"fast":              1,
}

var verdictColor = map[string]string{
	"invalid":           "#c586c0", // magenta — parse/type error
	"slow":              "#f14c4c", // red
	"moderate":          "#e5c07b", // amber
	"runtime_dependent": "#61afef", // blue — cost unknowable statically
	"fast":              "#98c379", // green
}

var (
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f7f7f"))
	descStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#9a9a9a"))
	headStyle  = lipgloss.NewStyle().Bold(true)
	codeStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#d7d7d7"))
	plainStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a"))
	okStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#98c379")).Bold(true)
)

func colorFor(verdict string) color.Color {
	if hex, ok := verdictColor[verdict]; ok {
		return lipgloss.Color(hex)
	}
	return lipgloss.Color("#7f7f7f")
}

// label turns "runtime_dependent" into "runtime dependent".
func label(verdict string) string {
	return strings.ReplaceAll(verdict, "_", " ")
}

// contentWidth is the usable text width inside the doc margins, derived from the
// last known terminal size (falling back to a sane default before the first
// WindowSizeMsg arrives).
func (m model) contentWidth() int {
	w := m.width
	if w == 0 {
		w = 80
	}
	w -= 6 // docStyle horizontal margins + a little breathing room
	if w > 100 {
		w = 100
	}
	if w < 24 {
		w = 24
	}
	return w
}

// resultsView renders the analysis: the original query with its expensive spans
// highlighted inline, a proportional cost bar, then one card per problematic
// sub-expression (worst-first) explaining why it's costly.
func (m model) resultsView() string {
	if m.results == nil {
		return ""
	}
	r := m.results
	w := m.contentWidth()

	var b strings.Builder

	b.WriteString(headStyle.Render("Query"))
	b.WriteString("\n")
	b.WriteString(highlightQuery(r.Query, r.Findings, w))
	b.WriteString("\n\n")

	b.WriteString(headStyle.Render("Cost breakdown"))
	b.WriteString("\n")
	b.WriteString(summaryBar(r.Summary))
	b.WriteString("\n\n")

	findings := append([]Finding(nil), r.Findings...)
	sort.SliceStable(findings, func(i, j int) bool {
		return verdictSeverity[findings[i].Verdict] > verdictSeverity[findings[j].Verdict]
	})

	shown := 0
	for _, f := range findings {
		// A finding with no codes is a clean sub-expression — not an
		// "expensive part", so leave it out of the breakdown.
		if len(f.Codes) == 0 {
			continue
		}
		b.WriteString(renderFinding(f, w))
		b.WriteString("\n")
		shown++
	}

	if shown == 0 {
		b.WriteString(okStyle.Render("✓ Nothing expensive — query looks clean."))
	}

	return docStyle.Render(b.String())
}

// highlightQuery echoes the query back with the spans of any problematic
// selector colored + underlined in its verdict color. Because the API gives us
// selector text rather than offsets, each problematic selector is located by
// substring; when spans overlap, the higher-severity verdict wins.
func highlightQuery(query string, findings []Finding, width int) string {
	if query == "" {
		return dimStyle.Render("(empty)")
	}

	sev := make([]int, len(query))
	verd := make([]string, len(query))
	for _, f := range findings {
		if len(f.Codes) == 0 || f.Selector == "" {
			continue
		}
		s := verdictSeverity[f.Verdict]
		from := 0
		for {
			i := strings.Index(query[from:], f.Selector)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(f.Selector)
			for j := start; j < end; j++ {
				if s > sev[j] {
					sev[j] = s
					verd[j] = f.Verdict
				}
			}
			from = start + 1 // keep searching for further occurrences
		}
	}

	var b strings.Builder
	for i := 0; i < len(query); {
		j := i
		for j < len(query) && sev[j] == sev[i] && verd[j] == verd[i] {
			j++
		}
		seg := query[i:j]
		if sev[i] == 0 {
			b.WriteString(plainStyle.Render(seg))
		} else {
			b.WriteString(lipgloss.NewStyle().
				Foreground(colorFor(verd[i])).Bold(true).Underline(true).
				Render(seg))
		}
		i = j
	}

	return lipgloss.NewStyle().Width(width).Render(b.String())
}

// summaryBar is a stacked proportional bar (one colored segment per verdict)
// followed by labeled counts, e.g.  ████████░░░░  ● 2 slow  ● 1 fast
func summaryBar(summary map[string]int) string {
	const width = 32
	total := 0
	for _, n := range summary {
		total += n
	}
	if total == 0 {
		return dimStyle.Render("(no findings)")
	}

	var bar strings.Builder
	var chips []string
	for _, v := range verdictOrder {
		n := summary[v]
		if n == 0 {
			continue
		}
		seg := n * width / total
		if seg == 0 {
			seg = 1
		}
		bar.WriteString(lipgloss.NewStyle().Foreground(colorFor(v)).Render(strings.Repeat("█", seg)))

		chip := lipgloss.NewStyle().Foreground(colorFor(v)).
			Render(fmt.Sprintf("● %d %s", n, label(v)))
		chips = append(chips, chip)
	}

	return bar.String() + "  " + strings.Join(chips, "  ")
}

// renderFinding draws one selector as a card with a colored left spine matching
// its verdict, the selector itself, and a bulleted list of reason codes.
func renderFinding(f Finding, width int) string {
	color := colorFor(f.Verdict)

	badge := lipgloss.NewStyle().Foreground(color).Bold(true).
		Render(strings.ToUpper(label(f.Verdict)))
	header := badge + dimStyle.Render("  ·  "+f.Category)

	lines := []string{header, codeStyle.Render(f.Selector)}
	for _, c := range f.Codes {
		title := lipgloss.NewStyle().Foreground(color).Render("• " + c.Title)
		lines = append(lines, title+dimStyle.Render("  ("+c.Code+")"))
		if c.Description != "" {
			lines = append(lines, descStyle.Render("  "+c.Description))
		}
	}

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.NormalBorder()).
		BorderLeft(true).
		BorderForeground(color).
		PaddingLeft(1).
		Width(width).
		Render(strings.Join(lines, "\n"))
}

// errorView renders a query/analysis failure as a bordered red box with a
// human title derived from the error kind, instead of dumping a raw Go error.
func (m model) errorView() string {
	if m.err == nil {
		return ""
	}
	w := m.contentWidth()

	title := "Request failed"
	msg := m.err.Error()

	var apiErr *APIError
	switch {
	case errors.As(m.err, &apiErr):
		switch {
		case apiErr.StatusCode == 400:
			title = "Empty query"
			msg = apiErr.Message
		case apiErr.StatusCode == 422:
			title = "Could not analyze query"
			// The 422 body repeats the title; drop the redundant prefix.
			msg = strings.TrimPrefix(apiErr.Message, "could not analyze query: ")
		case apiErr.StatusCode >= 500:
			title = fmt.Sprintf("pedantic.run server error (%d)", apiErr.StatusCode)
			// The server-side detail (e.g. "Internal Server Error") is noise;
			// tell the user what to actually do.
			msg = "The server failed to analyze this query. Try again — if it keeps happening, it's a bug worth reporting."
		default:
			title = fmt.Sprintf("Request failed (%d)", apiErr.StatusCode)
			msg = apiErr.Message
		}
	case errors.Is(m.err, context.DeadlineExceeded):
		title = "Request timed out"
		msg = "pedantic.run did not respond in time. Check your connection and try again."
	}

	red := lipgloss.Color("#f14c4c")
	content := lipgloss.NewStyle().Foreground(red).Bold(true).Render("✗ "+title) +
		"\n" + descStyle.Render(msg)

	return docStyle.Render(
		lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(red).
			Padding(0, 1).
			Width(w).
			Render(content),
	)
}
