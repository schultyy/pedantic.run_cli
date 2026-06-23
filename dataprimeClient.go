package main

import "context"

// DataPrimeResults is the analysis response from /api/dataprime/analyze. It
// shares the PromQL envelope — `query`, a `summary` verdict tally, and a
// `findings` list — but each finding is anchored to a pipeline *stage* rather
// than a selector, because DataPrime is a piped query language with no label
// selectors to point at.
type DataPrimeResults struct {
	Query    string             `json:"query"`
	Summary  map[string]int     `json:"summary"`
	Findings []DataPrimeFinding `json:"findings"`
}

// DataPrimeFinding is one rule hit on one pipeline stage. Unlike a PromQL
// Finding (which groups several Codes under one selector), a DataPrime finding
// carries exactly one Code. `Stage` is the command it fired on (e.g. "groupby",
// "filter") and `Detail` is the offending fragment (e.g. a high-cardinality
// path). The analyzer only emits slow/moderate/runtime_dependent verdicts; a
// clean query comes back with an empty Findings list.
type DataPrimeFinding struct {
	Stage   string `json:"stage"`
	Verdict string `json:"verdict"`
	Detail  string `json:"detail"`
	Code    Code   `json:"code"`
}

// RunDataPrime POSTs a DataPrime query to pedantic.run and returns the parsed
// analysis. A non-2xx response comes back as *APIError.
func RunDataPrime(ctx context.Context, query string) (*DataPrimeResults, error) {
	var out DataPrimeResults
	if err := postAnalyze(ctx, dataPrimeURL(), query, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
