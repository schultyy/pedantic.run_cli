package main

import (
	"fmt"
	"strconv"
	"strings"
)

// A Step is one line of the walkthrough: a fragment of the original query, a
// plain-English description of what it does, and any cost annotations that fired
// on it. The Explain view is just an ordered list of these, numbered in the
// order the engine evaluates them.
//
// The API hands us structure and cost verdicts but no human description of what
// each operation *means* — that semantic layer (the Detail text and the
// per-node phrasing below) is authored here, which is the whole point of the
// Explain view over the existing cost breakdown.
type Step struct {
	// Fragment is the slice of query this step covers, echoed back verbatim
	// (DataPrime) or reconstructed from the node (PromQL).
	Fragment string
	// Detail is the plain-English explanation of what this step does.
	Detail string
	// Codes are the cost findings on this step (empty for a clean step).
	Codes []Code
	// Verdict is the worst verdict among Codes (empty when there are none).
	Verdict string
	// Spans are the expensive substrings within Fragment, used to color the
	// costly parts of the fragment in the flow chart the same way the Cost view
	// highlights spans in the full query. Located by substring at render time.
	Spans []HighlightSpan
}

// HighlightSpan is a substring of a Step's Fragment that earned a cost verdict.
// The flow chart colors each occurrence of Text in its verdict color, mirroring
// the Cost view's inline span highlighting.
type HighlightSpan struct {
	Text    string
	Verdict string
}

// --- DataPrime ------------------------------------------------------------

// explainDataPrime turns a DataPrime pipeline into a top-to-bottom walkthrough,
// one step per stage. DataPrime reads left-to-right / top-to-bottom: each `|`
// stage takes the previous stage's rows and transforms them, so the source
// order *is* the evaluation order.
//
// Stage text is recovered by splitting the original query on `|` rather than
// re-printing the AST: the split lines up 1:1 with ast.stages and preserves the
// user's exact formatting.
func explainDataPrime(a *DataPrimeAST) []Step {
	fragments := splitPipeline(a.Query)

	steps := make([]Step, 0, len(a.AST.Stages))
	for i, st := range a.AST.Stages {
		frag := ""
		if i < len(fragments) {
			frag = fragments[i]
		}
		steps = append(steps, Step{
			Fragment: frag,
			Detail:   dataPrimeStageDetail(st),
		})
	}

	// Cost findings are attached to the first matching stage step that doesn't
	// already carry a finding, so two same-typed stages don't both inherit one
	// stage's verdict. The /analyze findings (a.Findings) are the same data the
	// Cost view shows and are the authoritative source; the AST endpoint's own
	// cross_stage_annotations are usually empty and only used as a fallback when
	// no analyze findings were fetched, so the two never double-count.
	if len(a.Findings) > 0 {
		for _, f := range a.Findings {
			attachFinding(a.AST.Stages, steps, f.Stage, f.Verdict, f.Code, f.Detail)
		}
	} else {
		for _, anno := range a.CrossStageAnnotations {
			detail := ""
			if anno.Detail != nil {
				detail = *anno.Detail
			}
			attachFinding(a.AST.Stages, steps, anno.Command, anno.Verdict, anno.Code, detail)
		}
	}

	return steps
}

// attachFinding records one cost finding (its code, verdict, and offending
// detail) on the stage step it fired on. Detail, when present, is also recorded
// as a highlight span so the flow chart can color just that part of the stage,
// the way the Cost view highlights expensive spans in the full query.
func attachFinding(stages []DPStage, steps []Step, command, verdict string, code Code, detail string) {
	idx := matchStage(stages, command, steps)
	if idx < 0 {
		return
	}
	steps[idx].Codes = append(steps[idx].Codes, code)
	if detail != "" {
		steps[idx].Spans = append(steps[idx].Spans, HighlightSpan{Text: detail, Verdict: verdict})
	}
	if worse(verdict, steps[idx].Verdict) {
		steps[idx].Verdict = verdict
	}
}

// matchStage finds the stage step a finding belongs to: the first stage of the
// finding's command that hasn't been claimed yet.
func matchStage(stages []DPStage, command string, steps []Step) int {
	for i, st := range stages {
		if st.Type == command && len(steps[i].Codes) == 0 {
			return i
		}
	}
	// Fall back to the first stage of that command even if already claimed.
	for i, st := range stages {
		if st.Type == command {
			return i
		}
	}
	return -1
}

// splitPipeline splits a DataPrime query into its stage fragments, trimming
// surrounding whitespace. A single `|` separates stages, but `||` is the logical
// OR operator and a `|` inside a quoted string is literal text — neither should
// split a stage, so a plain strings.Split on "|" is wrong (it would chop an
// `agg … || …` stage in half). This scans instead, tracking quote state and
// treating a doubled pipe as one OR token.
func splitPipeline(query string) []string {
	var out []string
	var seg strings.Builder
	var quote byte // 0 when outside a string, else the open quote char

	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case quote != 0:
			seg.WriteByte(c)
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
			seg.WriteByte(c)
		case c == '|':
			if i+1 < len(query) && query[i+1] == '|' {
				// "||" logical OR — keep both pipes in the current stage.
				seg.WriteString("||")
				i++
			} else {
				out = append(out, strings.TrimSpace(seg.String()))
				seg.Reset()
			}
		default:
			seg.WriteByte(c)
		}
	}
	out = append(out, strings.TrimSpace(seg.String()))
	return out
}

// dataPrimeStageDetail returns the plain-English description for one stage,
// keyed by command. Unknown commands fall back to a generic phrasing so a stage
// the analyzer adds later still renders something useful.
func dataPrimeStageDetail(st DPStage) string {
	switch st.Type {
	case "source":
		name := st.Name
		if name == "" {
			name = "the dataset"
		}
		return fmt.Sprintf("Start the pipeline by reading events from the %q dataset.", name)
	case "filter", "where":
		return "Keep only events matching the condition; everything else is dropped here, so later stages see fewer rows."
	case "groupby", "multigroupby":
		return "Bucket events into groups by the listed keys and compute the aggregates for each group. The output is one row per group, not per event."
	case "countby":
		return "Count events per distinct value of the listed keys — a groupby specialized to counting."
	case "orderby", "sortby":
		return "Sort the rows by the given keys. This needs the whole result set in hand, so it runs after everything upstream."
	case "limit", "top", "bottom":
		n := ""
		if st.Count != nil {
			n = strconv.Itoa(*st.Count) + " "
		}
		return fmt.Sprintf("Keep only the first %srows and discard the rest.", n)
	case "choose", "select":
		return "Project the output down to just the listed fields (and aliases); other fields are dropped from each row."
	case "dedupe", "distinct":
		return "Remove duplicate rows so each distinct combination of the keys appears once."
	case "extract":
		return "Parse new fields out of an existing value (e.g. a regex or key/value split) and add them to each row."
	case "enrich":
		return "Join each row against an enrichment table to attach extra context fields."
	case "create", "add":
		return "Compute a new field on each row from an expression."
	default:
		return fmt.Sprintf("Apply the %q operation to each row passing through the pipeline.", st.Type)
	}
}

// --- PromQL ---------------------------------------------------------------

// explainPromQL turns a PromQL expression tree into an inside-out walkthrough.
// PromQL evaluates from the innermost expression outward — you can't rate() a
// range vector before you've selected the series and taken the range — so a
// post-order traversal (children before parent) yields the steps in the order
// the engine actually computes them.
//
// Pure literal operands (bare numbers like the 10 in topk(10, …)) aren't
// operations, so they get no step of their own; they're folded into their
// parent's description instead.
func explainPromQL(a *PromQLAST) []Step {
	var steps []Step
	walkPromNode(&a.AST, &steps)
	return steps
}

// walkPromNode appends a step for `n` after recursing into its children, so the
// resulting slice is in evaluation (inside-out) order. Numbers are skipped.
func walkPromNode(n *PromNode, steps *[]Step) {
	if n == nil {
		return
	}
	switch n.Type {
	case "number":
		return // a literal operand, not an operation
	case "binop":
		walkPromNode(n.Left, steps)
		walkPromNode(n.Right, steps)
	case "range":
		walkPromNode(n.Selector, steps)
	case "call", "aggregate":
		for i := range n.Args {
			walkPromNode(&n.Args[i], steps)
		}
	}

	*steps = append(*steps, Step{
		Fragment: renderPromNode(n),
		Detail:   promNodeDetail(n),
		Codes:    promNodeCodes(n),
		Verdict:  promNodeVerdict(n),
		Spans:    collectPromSpans(n),
	})
}

// collectPromSpans gathers the expensive substrings to highlight within a node's
// fragment: every node in its subtree (itself included) that carries a cost
// verdict contributes its rendered text. Within a composite node's box this
// colors just the costly sub-expression — the same effect highlightQuery gives
// the full query in the Cost view.
func collectPromSpans(n *PromNode) []HighlightSpan {
	var spans []HighlightSpan
	var walk func(nd *PromNode)
	walk = func(nd *PromNode) {
		if nd == nil {
			return
		}
		if v := promNodeVerdict(nd); v != "" {
			spans = append(spans, HighlightSpan{Text: renderPromNode(nd), Verdict: v})
		}
		walk(nd.Left)
		walk(nd.Right)
		walk(nd.Selector)
		for i := range nd.Args {
			walk(&nd.Args[i])
		}
	}
	walk(n)
	return spans
}

// promNodeDetail returns the plain-English description of one PromQL node.
func promNodeDetail(n *PromNode) string {
	switch n.Type {
	case "selector":
		if len(n.Matchers) == 0 {
			return fmt.Sprintf("Select every time series for the metric %q (no label filters, so all of them).", n.Name)
		}
		return fmt.Sprintf("Select the time series for metric %q, keeping only those matching %s.", n.Name, matchersPhrase(n.Matchers))
	case "range":
		return fmt.Sprintf("Turn each series into a range vector: the raw samples within a sliding %s window. Required input for rate-style functions.", n.Duration)
	case "aggregate":
		grp := "across all series, collapsing them to a single value"
		if len(n.Labels) > 0 {
			if n.Grouping == "without" {
				grp = fmt.Sprintf("across series, dropping the %s label(s) and keeping the rest as groups", strings.Join(n.Labels, ", "))
			} else {
				grp = fmt.Sprintf("grouped by %s — one result series per distinct combination", strings.Join(n.Labels, ", "))
			}
		}
		return fmt.Sprintf("%s the values %s.", aggVerb(n.Name), grp)
	case "call":
		return promFuncDetail(n.Name, n.Args)
	case "binop":
		return binopDetail(n)
	default:
		return fmt.Sprintf("Evaluate the %q expression.", n.Type)
	}
}

// promFuncDetail describes a function call, with bespoke phrasing for the common
// functions and a generic fallback for the rest.
func promFuncDetail(name string, args []PromNode) string {
	switch name {
	case "rate":
		return "Compute the per-second average rate of increase of the counter over each range window, accounting for counter resets."
	case "irate":
		return "Compute the per-second instant rate from the last two samples in each window — reacts faster than rate(), noisier."
	case "increase":
		return "Compute the total increase of the counter over each range window (rate × window length)."
	case "delta":
		return "Compute the difference between the first and last sample in each window (for gauges)."
	case "histogram_quantile":
		q := ""
		if len(args) > 0 && args[0].Type == "number" {
			q = fmt.Sprintf(" (the %s quantile)", trimNum(args[0].Value))
		}
		return fmt.Sprintf("Estimate a quantile%s from a classic histogram's bucket counts. Expects the buckets aggregated by the `le` label.", q)
	case "sum_over_time", "avg_over_time", "min_over_time", "max_over_time", "count_over_time", "stddev_over_time", "last_over_time":
		op := strings.TrimSuffix(name, "_over_time")
		return fmt.Sprintf("Take the %s of each series' samples within every range window.", op)
	case "topk", "bottomk":
		k := ""
		if len(args) > 0 && args[0].Type == "number" {
			k = trimNum(args[0].Value) + " "
		}
		end := "largest"
		if name == "bottomk" {
			end = "smallest"
		}
		return fmt.Sprintf("Keep only the %sseries with the %s values; the rest are dropped. Must sort the full set first.", k, end)
	case "clamp_max", "clamp_min", "abs", "ceil", "floor", "round", "sqrt", "exp", "ln", "log2", "log10":
		return fmt.Sprintf("Apply the %s() math function to each sample.", name)
	case "sum", "avg", "min", "max", "count", "stddev", "stdvar", "group":
		// An aggregation operator used as a plain call (no by/without) collapses
		// every input series into a single result.
		return fmt.Sprintf("%s the values across all input series, collapsing them to a single series.", aggVerb(name))
	default:
		return fmt.Sprintf("Apply the %s() function to each input series.", name)
	}
}

// aggVerb maps an aggregation operator to an imperative verb for the sentence.
func aggVerb(name string) string {
	switch name {
	case "sum":
		return "Sum"
	case "avg":
		return "Average"
	case "min":
		return "Take the minimum of"
	case "max":
		return "Take the maximum of"
	case "count":
		return "Count"
	case "count_values":
		return "Count occurrences of each distinct value of"
	case "stddev":
		return "Take the standard deviation of"
	case "stdvar":
		return "Take the standard variance of"
	case "group":
		return "Group (value 1 per group)"
	default:
		return fmt.Sprintf("Apply %s to", name)
	}
}

// binopDetail describes a binary operation. The phrasing splits three ways:
// comparison operators filter, logical operators combine sets, and arithmetic
// operators compute — and each reads differently depending on whether one side
// is a scalar literal (the common "metric / 1024" or "metric > 0.9" shape) or
// two vectors matched by label.
func binopDetail(n *PromNode) string {
	scalar := scalarOperand(n)

	switch {
	case isCompareOp(n.Op):
		if scalar != "" {
			return fmt.Sprintf("Keep only samples whose value is %s %s; the rest are filtered out.", compareWord(n.Op), scalar)
		}
		return fmt.Sprintf("Keep only samples where the left side is %s the right, matching series by their labels.", compareWord(n.Op))
	case isLogicOp(n.Op):
		return logicDetail(n.Op)
	default: // arithmetic
		verb, prep := arithVerbPrep(n.Op)
		if scalar != "" {
			return fmt.Sprintf("%s each sample %s the scalar %s.", verb, prep, scalar)
		}
		return fmt.Sprintf("%s the two result sets, matching series by their labels.", verb)
	}
}

// scalarOperand returns the rendered value of a binop's scalar side, if exactly
// one side is a bare number; otherwise "".
func scalarOperand(n *PromNode) string {
	if n.Right != nil && n.Right.Type == "number" {
		return trimNum(n.Right.Value)
	}
	if n.Left != nil && n.Left.Type == "number" {
		return trimNum(n.Left.Value)
	}
	return ""
}

func isCompareOp(op string) bool {
	switch op {
	case "eq", "eql", "ne", "neq", "gt", "ge", "gte", "lt", "le", "lte":
		return true
	}
	return false
}

func isLogicOp(op string) bool {
	switch op {
	case "and", "or", "unless":
		return true
	}
	return false
}

func compareWord(op string) string {
	switch op {
	case "eq", "eql":
		return "equal to"
	case "ne", "neq":
		return "not equal to"
	case "gt":
		return "greater than"
	case "ge", "gte":
		return "≥"
	case "lt":
		return "less than"
	case "le", "lte":
		return "≤"
	default:
		return op
	}
}

func logicDetail(op string) string {
	switch op {
	case "and":
		return "Intersect the two sets: keep left-side series that also exist on the right (logical AND)."
	case "or":
		return "Union the two sets: all left-side series plus any right-side series not already present (logical OR)."
	case "unless":
		return "Keep left-side series that do NOT have a match on the right (set difference)."
	default:
		return "Combine the two sets."
	}
}

// arithVerbPrep returns the verb and the preposition that joins it to a scalar,
// e.g. ("Divide", "by") → "Divide each sample by the scalar 1024."
func arithVerbPrep(op string) (string, string) {
	switch op {
	case "add":
		return "Add", "to"
	case "sub", "subtract":
		return "Subtract", "from"
	case "mul", "multiply":
		return "Multiply", "by"
	case "div", "divide":
		return "Divide", "by"
	case "mod":
		return "Take the modulo of", "by"
	case "pow", "power":
		return "Raise", "to the power of"
	default:
		return "Combine", "with"
	}
}

// matchersPhrase renders a selector's label filters as a human phrase, e.g.
// `env equals "prod" and path matches "/api/.*"`.
func matchersPhrase(ms []PromMatcher) string {
	parts := make([]string, len(ms))
	for i, m := range ms {
		parts[i] = fmt.Sprintf("%s %s %q", m.Label, matcherVerb(m.Op), m.Value)
	}
	return strings.Join(parts, " and ")
}

func matcherVerb(op string) string {
	switch op {
	case "eq":
		return "equals"
	case "ne":
		return "is not"
	case "re":
		return "matches"
	case "nre":
		return "does not match"
	default:
		return op
	}
}

// renderPromNode reconstructs the PromQL source text for a node from the AST.
// The AST carries no source offsets, so each node prints itself back to
// PromQL-ish syntax — close enough to the user's input to anchor each step.
func renderPromNode(n *PromNode) string {
	if n == nil {
		return ""
	}
	switch n.Type {
	case "number":
		return trimNum(n.Value)
	case "selector":
		if len(n.Matchers) == 0 {
			return n.Name
		}
		ms := make([]string, len(n.Matchers))
		for i, m := range n.Matchers {
			ms[i] = m.Label + matcherOp(m.Op) + strconv.Quote(m.Value)
		}
		return n.Name + "{" + strings.Join(ms, ", ") + "}"
	case "range":
		return renderPromNode(n.Selector) + "[" + n.Duration + "]"
	case "call":
		return n.Name + "(" + renderArgs(n.Args) + ")"
	case "aggregate":
		head := n.Name
		if len(n.Labels) > 0 {
			grouping := n.Grouping
			if grouping == "" {
				grouping = "by"
			}
			head += " " + grouping + " (" + strings.Join(n.Labels, ", ") + ")"
		}
		return head + " (" + renderArgs(n.Args) + ")"
	case "binop":
		return renderPromNode(n.Left) + " " + binopSymbol(n.Op) + " " + renderPromNode(n.Right)
	default:
		return n.Type
	}
}

func renderArgs(args []PromNode) string {
	parts := make([]string, len(args))
	for i := range args {
		parts[i] = renderPromNode(&args[i])
	}
	return strings.Join(parts, ", ")
}

func matcherOp(op string) string {
	switch op {
	case "eq":
		return "="
	case "ne":
		return "!="
	case "re":
		return "=~"
	case "nre":
		return "!~"
	default:
		return "="
	}
}

func binopSymbol(op string) string {
	switch op {
	case "add":
		return "+"
	case "sub", "subtract":
		return "-"
	case "mul", "multiply":
		return "*"
	case "div", "divide":
		return "/"
	case "mod":
		return "%"
	case "pow", "power":
		return "^"
	case "eq", "eql":
		return "=="
	case "ne", "neq":
		return "!="
	case "gt":
		return ">"
	case "ge", "gte":
		return ">="
	case "lt":
		return "<"
	case "le", "lte":
		return "<="
	case "and":
		return "and"
	case "or":
		return "or"
	case "unless":
		return "unless"
	default:
		return op
	}
}

// promNodeCodes collects the cost codes attached to a node across all its
// annotation categories (matchers/range_vectors/aggregation); a clean node
// returns nil.
func promNodeCodes(n *PromNode) []Code {
	var codes []Code
	for _, a := range n.Annotations {
		codes = append(codes, a.Codes...)
	}
	return codes
}

// promNodeVerdict returns the worst verdict among a node's annotations that
// actually carry codes (a clean "fast"/"runtime_dependent" with no codes isn't
// a finding worth flagging on the step).
func promNodeVerdict(n *PromNode) string {
	worst := ""
	for _, a := range n.Annotations {
		if len(a.Codes) == 0 {
			continue
		}
		if worse(a.Verdict, worst) {
			worst = a.Verdict
		}
	}
	return worst
}

// worse reports whether verdict a is more severe than verdict b, using the
// shared verdictSeverity scale. An empty verdict is the least severe.
func worse(a, b string) bool {
	return verdictSeverity[a] > verdictSeverity[b]
}

// trimNum formats a float the way it was likely written: 10 not 10.000000,
// 0.99 not 0.990000.
func trimNum(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
