package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// baseURL is the pedantic.run PromQL analysis endpoint.
const baseURL = "https://pedantic.run/api/promql/analyze"

// Payload is the JSON body we POST to pedantic.run.
type Payload struct {
	Query string `json:"query"`
}

// PromQLResults is the analysis response. `summary` is a tally of how many
// findings landed in each verdict bucket (fast/moderate/slow/…); `findings`
// is one entry per analyzed sub-expression (selector).
type PromQLResults struct {
	Query    string         `json:"query"`
	Summary  map[string]int `json:"summary"`
	Findings []Finding      `json:"findings"`
}

// Finding is the verdict for a single selector within the query, plus the
// codes explaining why it earned that verdict. A finding with no codes is a
// clean sub-expression (nothing expensive about it).
type Finding struct {
	Category string `json:"category"`
	Selector string `json:"selector"`
	Verdict  string `json:"verdict"`
	Codes    []Code `json:"codes"`
}

// Code is a single rule hit: a stable machine code plus a human explanation.
type Code struct {
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// httpClient is shared and reused across requests (connection pooling).
var httpClient = &http.Client{Timeout: 15 * time.Second}

// RunPromQl POSTs a PromQL query to pedantic.run and returns the raw
// response body. Swap the []byte return for a typed struct once the
// response shape is known.
func RunPromQl(ctx context.Context, query string) (*PromQLResults, error) {
	data, err := json.Marshal(Payload{Query: query})
	if err != nil {
		return nil, fmt.Errorf("encoding payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pedantic.run returned %s: %s", resp.Status, resp.Body)
	}

	var out PromQLResults
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &out, nil
}
