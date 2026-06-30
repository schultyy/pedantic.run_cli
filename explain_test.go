package main

import (
	"context"
	"testing"
	"time"
)

// TestFlowChartRender builds a few steps by hand (no network) and prints the
// flow chart so the box/arrow layout can be eyeballed.
// Run with: go test -run FlowChartRender -v
func TestFlowChartRender(t *testing.T) {
	steps := []Step{
		{Fragment: `source logs`, Detail: "Start the pipeline by reading events from the \"logs\" dataset."},
		{
			Fragment: `groupby path, user_id aggregate count() as errs`,
			Detail:   "Bucket events into groups by the listed keys and compute the aggregates for each group. The output is one row per group, not per event.",
			Verdict:  "slow",
			Codes:    []Code{{Code: "DP001", Title: "High-cardinality grouping", Description: "Grouping by user_id can explode the number of groups."}},
			Spans:    []HighlightSpan{{Text: "user_id", Verdict: "slow"}},
		},
		{
			Fragment: `orderby errs desc`,
			Detail:   "Sort the rows by the given keys. This needs the whole result set in hand, so it runs after everything upstream.",
			Verdict:  "runtime_dependent",
			Codes:    []Code{{Code: "DP014", Title: "Sort cost depends on row count"}},
		},
		{Fragment: `limit 10`, Detail: "Keep only the first 10 rows and discard the rest."},
	}
	m := model{width: 84}
	t.Logf("\n%s", m.renderWalkthrough(steps, "Walkthrough", "DataPrime runs top to bottom."))
}

// TestExplainPromQL hits the live /api/promql/ast endpoint and prints the
// inside-out walkthrough for a few representative queries.
// Run with: go test -run ExplainPromQL -v
func TestExplainPromQL(t *testing.T) {
	if testing.Short() {
		t.Skip("hits the live pedantic.run endpoint; skipped in -short mode")
	}
	queries := []string{
		`topk(10, sum by (pod) (rate(container_cpu_usage_seconds_total{env="prod"}[5m])))`,
		`histogram_quantile(0.99, sum by (le) (rate(http_request_duration_seconds_bucket[5m])))`,
		`avg(node_memory) / 1024 > 0.9`,
	}

	for _, q := range queries {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ast, err := RunPromQLAST(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("query %q failed: %v", q, err)
		}
		m := model{width: 90}
		t.Logf("\n=== %s ===\n%s", q, m.renderWalkthrough(explainPromQL(ast), "Walkthrough", "PromQL evaluates inside out."))
	}
}

// TestExplainDataPrime hits the live /api/dataprime/ast endpoint and prints the
// top-to-bottom pipeline walkthrough.
// Run with: go test -run ExplainDataPrime -v
func TestExplainDataPrime(t *testing.T) {
	if testing.Short() {
		t.Skip("hits the live pedantic.run endpoint; skipped in -short mode")
	}
	queries := []string{
		`source logs | filter status >= 500 | groupby path, user_id aggregate count() as errs, avg(duration) | orderby errs desc | limit 10`,
		`source logs | choose path, status | filter status > 400`,
	}

	for _, q := range queries {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		ast, err := RunDataPrimeAST(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("query %q failed: %v", q, err)
		}
		m := model{width: 90}
		t.Logf("\n=== %s ===\n%s", q, m.renderWalkthrough(explainDataPrime(ast), "Walkthrough", "DataPrime runs top to bottom."))
	}
}
