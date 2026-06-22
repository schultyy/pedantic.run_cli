package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"
)

// baseURL is the pedantic.run PromQL analysis endpoint.
const baseURL = "https://pedantic.run/api/promql/analyze"

// userAgent identifies this CLI (and its version) to the backend so requests
// from the TUI can be distinguished from the web app and other clients.
// `version` is set at build time via -ldflags (see main.go).
func userAgent() string {
	return fmt.Sprintf("pedantic-run-cli/%s (%s; %s)", version, runtime.GOOS, runtime.GOARCH)
}

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

// APIError is a non-2xx response from pedantic.run. The server returns
// `{"error": "..."}` for 400 (empty query) and 422 (could not analyze), which
// we surface as Message so the UI can show something human instead of a raw
// status line.
type APIError struct {
	StatusCode int
	Status     string // e.g. "422 Unprocessable Entity"
	Message    string // parsed from the {"error": ...} body, if present
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("pedantic.run %s: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("pedantic.run returned %s", e.Status)
}

// parseErrorBody pulls a human message out of an error response, handling both
// the analyzer's own `{"error": "..."}` shape and Phoenix's default
// `{"errors": {"detail": "..."}}` (returned for 500s and the like). Falls back
// to the raw trimmed body when neither matches.
func parseErrorBody(body []byte) string {
	var single struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &single) == nil && single.Error != "" {
		return single.Error
	}

	var nested struct {
		Errors struct {
			Detail string `json:"detail"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Errors.Detail != "" {
		return nested.Errors.Detail
	}

	return strings.TrimSpace(string(body))
}

// httpClient is shared and reused across requests (connection pooling).
var httpClient = &http.Client{Timeout: 15 * time.Second}

// RunPromQl POSTs a PromQL query to pedantic.run and returns the parsed
// analysis. A non-2xx response comes back as *APIError.
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
	req.Header.Set("User-Agent", userAgent())

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, &APIError{
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
			Message:    parseErrorBody(body),
		}
	}

	var out PromQLResults
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &out, nil
}
