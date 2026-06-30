package main

import "context"

// The /api/{lang}/ast endpoints return the annotated parse tree for a query —
// the same cost annotations as /analyze, but attached to nodes of the syntax
// tree rather than flattened into a findings list. The "Explain" view walks
// this tree to narrate what each part of the query *does*, in evaluation order.

// astURL builds the AST endpoint for a language off the shared baseHost, the
// same way promQLURL / dataPrimeURL build the analyze endpoints.
func promQLASTURL() string    { return baseHost + "/api/promql/ast" }
func dataPrimeASTURL() string { return baseHost + "/api/dataprime/ast" }

// --- DataPrime AST --------------------------------------------------------

// DataPrimeAST is the response from /api/dataprime/ast. DataPrime is a linear
// pipeline, so cost findings aren't attached to individual AST nodes; they come
// back separately in CrossStageAnnotations, keyed by the command they fired on.
type DataPrimeAST struct {
	Query                 string        `json:"query"`
	CrossStageAnnotations []DPCrossAnno `json:"cross_stage_annotations"`
	AST                   DataPrimePipe `json:"ast"`

	// Findings is populated client-side (not from the AST response) with the
	// findings from the /analyze endpoint, so the Explain view colors stages from
	// the same source the Cost view does. The AST endpoint's
	// cross_stage_annotations are frequently empty for DataPrime, so the
	// walkthrough leans on these instead. Not serialized.
	Findings []DataPrimeFinding `json:"-"`
}

// DPCrossAnno is one cost finding tied to a pipeline command (e.g. "groupby").
// Detail is the offending fragment (e.g. the high-cardinality field) and may be
// null, so it's a pointer.
type DPCrossAnno struct {
	Code    Code    `json:"code"`
	Command string  `json:"command"`
	Detail  *string `json:"detail"`
	Verdict string  `json:"verdict"`
}

// DataPrimePipe is the pipeline itself: an ordered list of stages. The explainer
// only needs each stage's type (to title it and match annotations) plus a few
// shallow fields for richer phrasing; the literal text of each stage is
// recovered by splitting the source query on `|`, which lines up 1:1 with these
// stages.
type DataPrimePipe struct {
	Type   string    `json:"type"`
	Stages []DPStage `json:"stages"`
}

// DPStage is one pipeline command. Fields beyond Type are optional and only
// populated for the stage kinds that have them (Name for source, Count for
// limit); the rest of each stage's detail is read from the source fragment.
type DPStage struct {
	Type  string `json:"type"`
	Name  string `json:"name"`  // source
	Count *int   `json:"count"` // limit
}

// RunDataPrimeAST POSTs a DataPrime query to /api/dataprime/ast and returns the
// annotated tree. A non-2xx response comes back as *APIError.
func RunDataPrimeAST(ctx context.Context, query string) (*DataPrimeAST, error) {
	var out DataPrimeAST
	if err := postAnalyze(ctx, dataPrimeASTURL(), query, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --- PromQL AST -----------------------------------------------------------

// PromQLAST is the response from /api/promql/ast: the query plus a single root
// expression node. Cost annotations are attached to the nodes themselves
// (selectors and range vectors), not collected separately.
type PromQLAST struct {
	Query string   `json:"query"`
	AST   PromNode `json:"ast"`
}

// PromNode is one node of the PromQL expression tree. PromQL nodes are
// polymorphic, distinguished by Type ("call", "aggregate", "range", "selector",
// "number", "binop"); each variant uses a different subset of these fields. One
// flat struct keeps the recursive walk simple — unused fields stay zero.
type PromNode struct {
	Type string `json:"type"`

	// call / aggregate
	Name string     `json:"name"`
	Args []PromNode `json:"args"`

	// aggregate (sum/avg/… with grouping)
	Labels   []string `json:"labels"`
	Grouping string   `json:"grouping"` // "by" or "without"

	// number
	Value float64 `json:"value"`

	// binop
	Op    string    `json:"op"`
	Left  *PromNode `json:"left"`
	Right *PromNode `json:"right"`

	// range vector
	Selector *PromNode `json:"selector"`
	Duration string    `json:"duration"`

	// selector
	Matchers    []PromMatcher `json:"matchers"`
	Annotations []PromAnno    `json:"annotations"`
}

// PromMatcher is one label filter on a selector, e.g. env="prod" (op "eq") or
// path=~"/api/.*" (op "re").
type PromMatcher struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Op    string `json:"op"`
}

// PromAnno is the cost verdict for one analysis category (matchers,
// range_vectors, aggregation) on a node. An annotation with no Codes is a clean
// category; the Codes explain a non-clean verdict.
type PromAnno struct {
	Category string `json:"category"`
	Verdict  string `json:"verdict"`
	Codes    []Code `json:"codes"`
}

// RunPromQLAST POSTs a PromQL query to /api/promql/ast and returns the
// annotated tree. A non-2xx response comes back as *APIError.
func RunPromQLAST(ctx context.Context, query string) (*PromQLAST, error) {
	var out PromQLAST
	if err := postAnalyze(ctx, promQLASTURL(), query, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
