package main

import (
	"fmt"
	"image/color"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
)

// Verdicts ordered most→least expensive. Drives both the summary order and the
// sort of the findings list, so the costly stuff is always on top.
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
	dimStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f7f7f"))
	descStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9a9a9a"))
	headStyle = lipgloss.NewStyle().Bold(true)
	codeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#d7d7d7"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#98c379")).Bold(true)
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

// resultsView renders the analysis as a visual cost breakdown: a proportion bar
// + verdict tally up top, then one card per expensive sub-expression (sorted
// worst-first) with the offending selector and the reasons it's costly.
func (m model) resultsView() string {
	if m.results == nil {
		return ""
	}
	r := m.results

	var b strings.Builder
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
		b.WriteString(renderFinding(f))
		b.WriteString("\n")
		shown++
	}

	if shown == 0 {
		b.WriteString(okStyle.Render("✓ Nothing expensive — query looks clean."))
	}

	return docStyle.Render(b.String())
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
func renderFinding(f Finding) string {
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
		Render(strings.Join(lines, "\n"))
}
