package main

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// numStyle renders the step number badge for a clean step (dim); steps with a
// finding recolor it to the verdict color instead.
var numStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#7f7f7f")).Bold(true)

// explainView renders the active tab's Explain walkthrough using the AST loaded
// for its language. It mirrors resultsView's dispatch on tab.lang.
func (m model) explainView(t tab) string {
	switch t.lang {
	case langDataPrime:
		if t.dpAST == nil {
			return ""
		}
		return m.renderWalkthrough(
			explainDataPrime(t.dpAST),
			"Walkthrough", "DataPrime runs top to bottom — each stage reshapes the rows the previous one produced.",
		)
	default:
		if t.promAST == nil {
			return ""
		}
		return m.renderWalkthrough(
			explainPromQL(t.promAST),
			"Walkthrough", "PromQL evaluates inside out — innermost expression first, each step feeding the next.",
		)
	}
}

// renderWalkthrough draws the steps as a top-to-bottom flow chart: one bordered
// node per step, joined by arrows in evaluation order. The border and number
// badge are colored by the step's worst verdict, so an expensive stage reads as
// a red box in the chain at a glance; clean steps get a dim border. The
// fragment, its plain-English detail, and any cost findings all live inside the
// node.
func (m model) renderWalkthrough(steps []Step, heading, subtitle string) string {
	w := m.contentWidth()

	var b strings.Builder
	b.WriteString(headStyle.Render(heading))
	b.WriteString("  ")
	b.WriteString(dimStyle.Render(fmt.Sprintf("· %d steps", len(steps))))
	b.WriteString("\n")
	b.WriteString(descStyle.Render(subtitle))
	b.WriteString("\n\n")

	if len(steps) == 0 {
		b.WriteString(dimStyle.Render("(nothing to explain)"))
		return docStyle.Render(b.String())
	}

	// Every node is the same width so the chain reads as a single column. boxW is
	// the bordered, on-screen width; textW is the wrappable area inside the border
	// and its 1-cell horizontal padding (2 border + 2 padding = 4 cells).
	boxW := w - 2
	if boxW < 24 {
		boxW = 24
	}
	textW := boxW - 4
	if textW < 16 {
		textW = 16
	}

	for i, s := range steps {
		b.WriteString(flowNode(i+1, s, textW))
		b.WriteString("\n")
		if i < len(steps)-1 {
			b.WriteString(flowConnector(boxW))
			b.WriteString("\n")
		}
	}

	return docStyle.Render(b.String())
}

// flowNode renders one step as a bordered box. textW is the inner content width;
// the border and padding add 4 cells around it.
func flowNode(n int, s Step, textW int) string {
	var border color.Color = lipgloss.Color("#3a3a3a")
	num := numStyle
	if s.Verdict != "" {
		c := colorFor(s.Verdict)
		border = c
		num = numStyle.Foreground(c)
	}

	// Node header: number badge, then the query fragment this step covers, with
	// its expensive substrings colored in their verdict color.
	fragment := highlightFragment(s.Fragment, s.Spans, codeStyle.Bold(true))
	header := num.Render(fmt.Sprintf("%d", n)) + dimStyle.Render(" · ") + fragment
	lines := []string{header}

	// Plain-English detail, wrapped to the node's inner width.
	detail := lipgloss.NewStyle().Width(textW).Foreground(lipgloss.Color("#b0b0b0")).Render(s.Detail)
	lines = append(lines, detail)

	// Cost findings, if any, sit inside the node beneath the detail.
	for _, c := range s.Codes {
		badge := lipgloss.NewStyle().Foreground(colorFor(s.Verdict)).Bold(true).
			Render("⚠ " + strings.ToUpper(label(s.Verdict)))
		title := lipgloss.NewStyle().Foreground(colorFor(s.Verdict)).Render(c.Title)
		lines = append(lines, badge+dimStyle.Render(" · ")+title+dimStyle.Render(" ("+c.Code+")"))
		if c.Description != "" {
			desc := lipgloss.NewStyle().Width(textW).Foreground(lipgloss.Color("#8a8a8a")).Render(c.Description)
			lines = append(lines, desc)
		}
	}

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(textW + 2). // content area incl. padding; border adds 2 more
		Render(strings.Join(lines, "\n"))
}

// flowConnector draws the arrow joining one node to the next, centered under a
// box of width boxW so it lines up with the column of nodes.
func flowConnector(boxW int) string {
	pad := strings.Repeat(" ", boxW/2)
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("#5a5a5a"))
	return pad + style.Render("│") + "\n" + pad + style.Render("▼")
}

// highlightFragment renders `text` with each span colored + underlined in its
// verdict color (higher severity winning where spans overlap), and everything
// else drawn with `base`. Spans are located by substring, so a span that isn't
// present in `text` simply doesn't highlight. This is the shared core behind the
// Cost view's highlightQuery and the flow chart's per-node fragment coloring.
func highlightFragment(text string, spans []HighlightSpan, base lipgloss.Style) string {
	if len(spans) == 0 || text == "" {
		return base.Render(text)
	}

	sev := make([]int, len(text))
	verd := make([]string, len(text))
	for _, sp := range spans {
		if sp.Text == "" {
			continue
		}
		s := verdictSeverity[sp.Verdict]
		from := 0
		for {
			i := strings.Index(text[from:], sp.Text)
			if i < 0 {
				break
			}
			start := from + i
			end := start + len(sp.Text)
			for j := start; j < end; j++ {
				if s > sev[j] {
					sev[j] = s
					verd[j] = sp.Verdict
				}
			}
			from = start + 1 // keep searching for further occurrences
		}
	}

	var b strings.Builder
	for i := 0; i < len(text); {
		j := i
		for j < len(text) && sev[j] == sev[i] && verd[j] == verd[i] {
			j++
		}
		seg := text[i:j]
		if sev[i] == 0 {
			b.WriteString(base.Render(seg))
		} else {
			b.WriteString(lipgloss.NewStyle().
				Foreground(colorFor(verd[i])).Bold(true).Underline(true).
				Render(seg))
		}
		i = j
	}
	return b.String()
}
