package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/url"
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
	host := flag.String("host", defaultBaseHost, "Base host for the pedantic.run API, e.g. http://localhost:4000")
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.BoolVar(showVersion, "v", false, "Print version and exit (shorthand)")
	promqlQuery := flag.String("promql", "", "Run a PromQL query non-interactively and exit")
	dataPrimeQuery := flag.String("dataprime", "", "Run a DataPrime query non-interactively and exit")
	outputMode := flag.String("output", "agents", "Output format for non-interactive mode: agents or json")
	flag.StringVar(outputMode, "o", "agents", "Output format shorthand")
	flag.Parse()

	// Keep the existing `pedantic version` subcommand working alongside the
	// --version / -v flags.
	if *showVersion || flag.Arg(0) == "version" {
		fmt.Println("pedantic", version)
		return
	}

	normalized, err := normalizeHost(*host)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid --host:", err)
		os.Exit(2)
	}
	baseHost = normalized

	if *promqlQuery != "" || *dataPrimeQuery != "" {
		runNonInteractive(*promqlQuery, *dataPrimeQuery, *outputMode)
		return
	}

	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

func runNonInteractive(promqlQuery, dataPrimeQuery, outputMode string) {
	if promqlQuery != "" && dataPrimeQuery != "" {
		fmt.Fprintln(os.Stderr, "cannot use --promql and --dataprime together")
		os.Exit(2)
	}
	if outputMode != "json" && outputMode != "agents" {
		fmt.Fprintf(os.Stderr, "invalid --output value %q: must be json or agents\n", outputMode)
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var promRes *PromQLResults
	var dpRes *DataPrimeResults
	var runErr error

	if promqlQuery != "" {
		promRes, runErr = RunPromQl(ctx, promqlQuery)
	} else {
		if !features.DataPrime {
			fmt.Fprintln(os.Stderr, "--dataprime is not available in this build")
			os.Exit(2)
		}
		dpRes, runErr = RunDataPrime(ctx, dataPrimeQuery)
	}

	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}

	switch outputMode {
	case "json":
		var v any
		if promRes != nil {
			v = promRes
		} else {
			v = dpRes
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			fmt.Fprintln(os.Stderr, "encoding JSON:", err)
			os.Exit(1)
		}
	default: // agents
		m := model{width: 100}
		if promRes != nil {
			fmt.Println(m.promResultsView(promRes))
		} else {
			fmt.Println(m.dataPrimeResultsView(dpRes))
		}
	}
}

// normalizeHost validates a --host override and strips any trailing slash so it
// joins cleanly with the endpoint paths. It requires an http/https scheme and a
// host, so a typo fails fast with a clear message instead of surfacing later as
// a confusing request error.
func normalizeHost(h string) (string, error) {
	h = strings.TrimRight(strings.TrimSpace(h), "/")
	u, err := url.Parse(h)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("must start with http:// or https:// (got %q)", h)
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host (got %q)", h)
	}
	return h, nil
}

// language is the query language a tab analyzes. PromQL and DataPrime are
// different enough — selectors vs. pipeline stages — that each gets its own
// tab, editor, and results renderer rather than a shared, lowest-common screen.
type language int

const (
	langPromQL language = iota
	langDataPrime
)

// viewMode is which lens the results pane shows for a tab. Cost is the static
// cost breakdown (the /analyze endpoints); Explain is the inside-out / top-down
// walkthrough of what the query does (the /ast endpoints).
type viewMode int

const (
	modeCost viewMode = iota
	modeExplain
)

// tab is one query language's full state: its editor and the latest analysis
// (or error) for whatever's in that editor. Each tab keeps its own content, so
// switching languages never clobbers the other's query or results.
type tab struct {
	lang     language
	title    string
	textarea textarea.Model
	mode     viewMode

	// Exactly one of prom/dp is ever populated, per lang; err is set instead
	// when the last run failed.
	prom *PromQLResults
	dp   *DataPrimeResults
	err  error

	// AST results backing the Explain view, fetched lazily when the user first
	// switches a tab into Explain mode. astErr holds a failed AST fetch.
	promAST *PromQLAST
	dpAST   *DataPrimeAST
	astErr  error
}

type model struct {
	tabs   []tab
	active int
	width  int
}

func initialModel() model {
	prom := newEditor(`up{host="foo"}`)
	prom.Focus() // the active tab starts focused; any other stays blurred

	tabs := []tab{
		{lang: langPromQL, title: "PromQL", textarea: prom},
	}

	// DataPrime is gated behind a compile-time feature flag (see features.go):
	// released builds ship with it off, so the tab only exists when the embedded
	// features.json turns it on.
	if features.DataPrime {
		dp := newEditor(`source logs | groupby path aggregate count()`)
		tabs = append(tabs, tab{lang: langDataPrime, title: "DataPrime", textarea: dp})
	}

	return model{tabs: tabs}
}

// newEditor builds a textarea configured the way both tabs want it, differing
// only in placeholder.
func newEditor(placeholder string) textarea.Model {
	ti := textarea.New()
	ti.Placeholder = placeholder
	ti.ShowLineNumbers = true
	ti.DynamicHeight = true
	ti.MinHeight = 3
	ti.MaxHeight = 15
	ti.MaxContentHeight = 20
	ti.SetWidth(1000)
	ti.SetVirtualCursor(false)
	return ti
}

// queryResultMsg carries an analysis back to the tab that requested it. `tab`
// is the index that ran the query, so a result still lands on the right tab
// even if the user switched tabs while the request was in flight.
type queryResultMsg struct {
	tab  int
	prom *PromQLResults
	dp   *DataPrimeResults
	err  error
}

// astResultMsg carries a fetched AST back to the tab that requested it, routed
// the same way as queryResultMsg so it lands on the right tab regardless of
// what's focused when it returns.
type astResultMsg struct {
	tab  int
	prom *PromQLAST
	dp   *DataPrimeAST
	err  error
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tea.RequestBackgroundColor)
}

// runQueryCommand dispatches to the right client for the tab's language. The
// tab index and language are captured up front so the result is routed back
// correctly regardless of what's focused when it returns.
func runQueryCommand(idx int, lang language, query string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return queryResultMsg{tab: idx}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		switch lang {
		case langDataPrime:
			res, err := RunDataPrime(ctx, query)
			return queryResultMsg{tab: idx, dp: res, err: err}
		default:
			res, err := RunPromQl(ctx, query)
			return queryResultMsg{tab: idx, prom: res, err: err}
		}
	}
}

// runASTCommand fetches the annotated AST for the Explain view, mirroring
// runQueryCommand: tab index and language are captured up front so the result
// routes back to the right tab.
func runASTCommand(idx int, lang language, query string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return astResultMsg{tab: idx}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		switch lang {
		case langDataPrime:
			res, err := RunDataPrimeAST(ctx, query)
			// The AST endpoint's cross_stage_annotations are usually empty for
			// DataPrime, so pull the cost findings from /analyze (the same source
			// the Cost view uses) and let the walkthrough color stages from them.
			// A failed analyze just means an uncolored walkthrough, not an error.
			if err == nil {
				if a, aErr := RunDataPrime(ctx, query); aErr == nil {
					res.Findings = a.Findings
				}
			}
			return astResultMsg{tab: idx, dp: res, err: err}
		default:
			res, err := RunPromQLAST(ctx, query)
			return astResultMsg{tab: idx, prom: res, err: err}
		}
	}
}

// runActive dispatches the request the active tab's current mode needs — cost
// analysis or AST — so ctrl+enter always runs whatever the visible pane shows.
// It also drops the *other* mode's cached result for this tab: the query just
// changed, so that pane is now stale and must refetch (lazily, via toggleMode)
// rather than show a breakdown for an older query.
func (m *model) runActive() tea.Cmd {
	t := &m.tabs[m.active]
	q := t.textarea.Value()
	if t.mode == modeExplain {
		t.prom, t.dp, t.err = nil, nil, nil
		return runASTCommand(m.active, t.lang, q)
	}
	t.promAST, t.dpAST, t.astErr = nil, nil, nil
	return runQueryCommand(m.active, t.lang, q)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.BackgroundColorMsg:
		styles := textarea.DefaultStyles(msg.IsDark())
		for i := range m.tabs {
			m.tabs[i].textarea.SetStyles(styles)
		}
	case queryResultMsg:
		t := &m.tabs[msg.tab]
		// Check the error before touching results — on the error path both
		// result pointers are nil.
		if msg.err != nil {
			t.err, t.prom, t.dp = msg.err, nil, nil
		} else {
			t.err, t.prom, t.dp = nil, msg.prom, msg.dp
		}
		return m, nil
	case astResultMsg:
		t := &m.tabs[msg.tab]
		if msg.err != nil {
			t.astErr, t.promAST, t.dpAST = msg.err, nil, nil
		} else {
			t.astErr, t.promAST, t.dpAST = nil, msg.prom, msg.dp
		}
		return m, nil
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "tab":
			return m, m.switchTab(1)
		case "shift+tab":
			return m, m.switchTab(-1)
		case "ctrl+enter":
			return m, m.runActive()
		case "ctrl+e":
			return m, m.toggleMode()
		case "ctrl+backspace":
			m.tabs[m.active].textarea.SetValue("")
			return m, nil
		}
	}

	// Everything else (typing, cursor moves, blink) goes to the focused editor.
	var cmd tea.Cmd
	m.tabs[m.active].textarea, cmd = m.tabs[m.active].textarea.Update(msg)
	return m, cmd
}

// toggleMode flips the active tab between the Cost and Explain panes, then
// lazily fetches the entered pane's data if it isn't loaded yet (e.g. on first
// switch, or after a re-run invalidated it), so the pane reflects the current
// query without a separate run.
func (m *model) toggleMode() tea.Cmd {
	t := &m.tabs[m.active]
	if t.mode == modeCost {
		t.mode = modeExplain
	} else {
		t.mode = modeCost
	}
	return m.fetchIfStale()
}

// fetchIfStale returns the command to fetch the active tab's current-mode data
// when it's missing (and the editor isn't empty), and nil otherwise. A prior
// error counts as "loaded" — we don't silently retry a query the server already
// rejected. This is what makes a mode show fresh results after the other mode's
// re-run cleared its cache.
func (m model) fetchIfStale() tea.Cmd {
	t := m.tabs[m.active]
	if strings.TrimSpace(t.textarea.Value()) == "" {
		return nil
	}
	if t.mode == modeExplain {
		if t.astErr == nil && ((t.lang == langDataPrime && t.dpAST == nil) || (t.lang == langPromQL && t.promAST == nil)) {
			return runASTCommand(m.active, t.lang, t.textarea.Value())
		}
		return nil
	}
	if t.err == nil && ((t.lang == langDataPrime && t.dp == nil) || (t.lang == langPromQL && t.prom == nil)) {
		return runQueryCommand(m.active, t.lang, t.textarea.Value())
	}
	return nil
}

// switchTab moves focus by `delta` tabs (wrapping), blurring the old editor and
// focusing the new one so only the visible tab shows a cursor.
func (m *model) switchTab(delta int) tea.Cmd {
	m.tabs[m.active].textarea.Blur()
	n := len(m.tabs)
	m.active = (m.active + delta%n + n) % n
	return m.tabs[m.active].textarea.Focus()
}

func (m model) View() tea.View {
	active := m.tabs[m.active]

	// Everything above the editor; its rendered height tells us how far down to
	// push the cursor (one row per newline before the editor begins). With a
	// single tab there's nothing to switch between, so we skip the tab bar.
	prefix := "\n"
	if len(m.tabs) > 1 {
		prefix += m.renderTabs() + "\n"
	}

	sections := []string{active.textarea.View()}
	if active.mode == modeExplain {
		if active.astErr != nil {
			sections = append(sections, m.errorView(active.astErr))
		} else if ex := m.explainView(active); ex != "" {
			sections = append(sections, ex)
		}
	} else if active.err != nil {
		sections = append(sections, m.errorView(active.err))
	} else if res := m.resultsView(active); res != "" {
		sections = append(sections, res)
	}
	sections = append(sections, "\n"+m.footer())

	f := prefix + strings.Join(sections, "\n")

	var c *tea.Cursor
	if !active.textarea.VirtualCursor() {
		c = active.textarea.Cursor()
		c.Y += strings.Count(prefix, "\n")
	}

	v := tea.NewView(f)
	v.Cursor = c
	return v
}

// footer lists the active keybindings. The language-switch hint only appears
// when there's more than one tab to switch between.
func (m model) footer() string {
	toggle := "ctrl+e to explain"
	if m.tabs[m.active].mode == modeExplain {
		toggle = "ctrl+e for cost"
	}
	hints := []string{"ctrl+enter to run", toggle}
	if len(m.tabs) > 1 {
		hints = append(hints, "tab to switch language")
	}
	hints = append(hints, "ctrl+backspace to reset editor", "ctrl+c to quit")
	return "(" + strings.Join(hints, " · ") + ")"
}
