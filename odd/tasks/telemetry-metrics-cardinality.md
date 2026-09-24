# Telemetry collector /metrics cardinality

Locator: `odd/tasks/telemetry-metrics-cardinality.md` (worktree `gentle-ai-worktrees/telemetry-metrics-cardinality`, branch `fix/telemetry-metrics-cardinality` from `origin/main` 465452ebe). Engram mirror: `odd/telemetry-metrics-cardinality/tasks`.

## Objective
Keep the collector's `GET /metrics` exposition (and the collector's memory) bounded so it never crosses VictoriaMetrics' `-promscrape.maxScrapeSize` (64 MiB).

## Problem
Watchdog alert 2026-09-24: `/metrics` = 50.4 MB (75% of 64 MiB), growing linearly ~7.5 MB/day since the 2026-09-18 restart; the cap is reached in ~2 days, after which the whole scrape is dropped and every runtime panel reads 0. Collector RSS 720 MB on a 3.6 GB VPS.

Evidence (production snapshot, 259,217 series):
- `internal/telemetrycollector/metrics.go` `RuntimeMetrics` never evicts a series (no timestamp, reset only by restart). `provider`/`model` labels are client free-form: 355 providers, 637 models, 8,713 base combinations.
- `add()` creates a series even when `delta == 0`: 125,281 series (48%) = 24.8 MB are permanently 0 (token_fields_total 87,422; tokens_total 20,517; launches/duration ~17,200; responses 134).

## Why
Raising the cap only moves the limit to VPS RAM (collector + VictoriaMetrics both grow with series). The registry has to bound itself.

## Scope
- Authorized: `internal/telemetrycollector/metrics.go`, its tests, `cmd/gentle-telemetry/main.go` flag wiring and its tests, `docs/telemetry-collector.md`.
- Out of scope: bucketing provider/model into a closed set (product decision, loses long-tail detail), dashboard changes, VPS deploy (user decision after merge).

## Constraints
- Counters stay monotonic while a series lives; eviction is a counter reset downstream, already handled by `increase()`/`rate()`.
- TTL must be far above the 15 s scrape interval so the last increment is always scraped before eviction.
- Strict TDD (openspec/config.yaml `strict_tdd: true`). Runner: `go test ./internal/telemetrycollector/... ./cmd/gentle-telemetry/...`.
- Delivery strategy: `ask-on-risk`; forecast ~200 authored lines, single PR.

## Tasks
- [x] T1 — Skip creating a series for a zero delta (existing series unchanged). Route: delegated direct (writer covers T1+T2: 2+ non-trivial files).
- [ ] T2 — Idle-series TTL: `lastUpdate` per series, evict idle series during `WriteTo`, injectable clock, `--runtime-metrics-ttl` flag (default 24h, `0` disables), doc update. Route: delegated direct (same writer).

## Acceptance criteria
- A delta of 0 on an absent series renders nothing; a delta of 0 on an existing series leaves it rendered unchanged.
- A series not updated for longer than the TTL is absent from the next `WriteTo`; an updated series is kept; TTL 0 never evicts.
- A series evicted and observed again restarts from its new delta.
- Docs describe both behaviors and the flag.

## Checks
- `go test ./internal/telemetrycollector/... ./cmd/gentle-telemetry/...`
- `go vet ./internal/telemetrycollector/... ./cmd/gentle-telemetry/...`
- `gofmt -l internal/telemetrycollector cmd/gentle-telemetry`

## Progress
- 2026-09-24: diagnosis done on the VPS (read-only), worktree created, document created.
- T1 done: zero deltas no longer create series (writer, strict TDD RED->GREEN observed by writer). Checks: go test, go vet, gofmt -l all clean; parent spot check `go test` ok.

## Next step
Delegate T1+T2 to one writer, two work-unit commits.
