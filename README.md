# pedantic.run CLI

A terminal UI for [pedantic.run](https://pedantic.run) — a static **PromQL cost
analyzer**. Type a PromQL query, run it, and get a colored breakdown of which
parts of the query are expensive and *why*, without ever touching your
Prometheus instance.

The analyzer is static: it inspects the structure of the query (matchers, range
vectors, aggregations, subqueries, joins) and flags patterns that tend to be
slow. It never executes the query, so there's no data and no series counts —
some costs are therefore reported as `runtime_dependent` (clean syntax, real
cost unknowable without live cardinality).

## What it looks like

Enter a query and press `ctrl+enter`:

```
topk(10, sum by (pod) (rate(container_cpu_usage_seconds_total{env="prod"}[5m])))

Cost breakdown
████████████████  ● 1 slow  ● 1 moderate  ● 2 fast

│ SLOW  ·  aggregation
│ container_cpu_usage_seconds_total{env="prod"}
│ • High-cardinality grouping  (HIGH_CARD_GROUPING)
│   Grouping by an unbounded label (request_id, trace_id, pod, …).
│ • Sorting aggregation  (SORT_AGGREGATION)
│   topk / bottomk / quantile must sort the full result set.
```

Each finding is one sub-expression of your query, with a verdict, the offending
selector, and the reasons it earned that verdict.

### Verdicts

| Verdict             | Meaning                                                        |
| ------------------- | ------------------------------------------------------------- |
| `fast`              | Cheap — nothing to worry about.                               |
| `moderate`          | Some cost worth being aware of.                               |
| `slow`              | Expensive pattern; likely a query to rework.                  |
| `runtime_dependent` | Syntactically clean; real cost depends on live cardinality.   |
| `invalid`           | The query failed to parse or type-check.                      |

## Install

Download a prebuilt binary from the [releases page](https://github.com/schultyy/pedantic.run_cli/releases)
(Linux and macOS, amd64 and arm64), or build from source:

```sh
go install github.com/schultyy/pedantic.run_cli@latest
```

## Usage

```sh
pedantic            # launch the TUI
pedantic --version  # print the version
```

Keys:

| Key          | Action          |
| ------------ | --------------- |
| `ctrl+enter` | run the query   |
| `ctrl+c`     | quit            |

## Development

```sh
go run .                 # run the TUI locally
go build ./...           # build
go test -short ./...     # tests (skips the live-endpoint test)
go test -v ./...         # includes a test that hits the live API
```

The HTTP client lives in `apiClient.go` (it POSTs `{"query": "..."}` to
`https://pedantic.run/api/promql/analyze`); the visual breakdown lives in
`results_view.go`; the Bubble Tea program is in `main.go`.

## Releases

Releases are built by [GoReleaser](https://goreleaser.com) and published to
GitHub Releases when a version tag is pushed:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## Built with

- [Bubble Tea](https://github.com/charmbracelet/bubbletea) — the TUI framework
- [Lip Gloss](https://github.com/charmbracelet/lipgloss) — styling and layout
