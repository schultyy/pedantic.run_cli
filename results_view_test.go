package main

import (
	"context"
	"testing"
	"time"
)

// TestRenderExpensiveQueries hits the live pedantic.run endpoint with a few
// expensive queries lifted from the perf corpus and prints the rendered cost
// breakdown. Run with: go test -run RenderExpensive -v
func TestRenderExpensiveQueries(t *testing.T) {
	if testing.Short() {
		t.Skip("hits the live pedantic.run endpoint; skipped in -short mode")
	}
	queries := []string{
		`http_requests_total{handler=~".*checkout.*"}`,
		`topk(10, sum by (pod) (rate(container_cpu_usage_seconds_total{env="prod"}[5m])))`,
		`avg_over_time(max_over_time(rate(http_requests_total{job="api"}[5m])[10m:1m])[1h:5m])`,
		`http_requests_total{job="api", code="200"}`,
	}

	for _, q := range queries {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		res, err := RunPromQl(ctx, q)
		cancel()
		if err != nil {
			t.Fatalf("query %q failed: %v", q, err)
		}
		m := model{width: 90}
		t.Logf("\n=== %s ===\n%s", q, m.promResultsView(res))
	}
}

// TestRenderError exercises the styled error box against a query the analyzer
// rejects (HTTP 422).
func TestRenderError(t *testing.T) {
	if testing.Short() {
		t.Skip("hits the live pedantic.run endpoint; skipped in -short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	_, err := RunPromQl(ctx, "this is not promql {{{")
	if err == nil {
		t.Fatal("expected an error for an invalid query")
	}
	m := model{width: 90}
	t.Logf("\nerr type: %T\n%s", err, m.errorView(err))
}
