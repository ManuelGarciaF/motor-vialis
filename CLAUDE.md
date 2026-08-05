# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Vialis Motor is a Go REST service that simulates a proposed public transport
line before it exists. Given an ordered list of stops and the exact geometry
between them, it estimates potential demand, distance, travel time, and
potential fare revenue. It evaluates a route someone else designed — it does
not generate routes or place stops itself.

Read `docs/arquitectura_motor.md` before making any change to demand,
travel-time, or revenue logic — it is the authoritative functional spec
(in Spanish) covering accessibility math, GTFS reference selection, confidence
rules, and the rationale behind design decisions like "one direction per
simulation" and "reject invalid geometry instead of auto-correcting it".
`sql/recorridos/README.md` and `sql/viajes/README.md` document the GTFS and
mobility-data ETL pipelines in the same depth.

## Commands

```bash
go build ./...
go vet ./...
gofmt -l .                 # must be empty; this repo has no separate lint step

go test ./...              # unit tests (fast, no DB required)
go test ./... -run TestName -v   # single test
go test ./internal/simulation/demand/...   # single package

# Integration tests hit a real PostgreSQL+PostGIS+H3 database and are skipped
# unless TEST_DATABASE_URL is set:
TEST_DATABASE_URL=postgresql://postgres:postgres@localhost:5432/vialis go test ./internal/database/postgres/...

# Run the API service (checks DB connectivity on startup, then serves /health)
go run ./cmd/api

# Run a single simulation from a JSON route file (see examples/linea-132.json)
go run ./cmd/simulation-test -route-file ./examples/linea-132.json
```

Default local DB: `postgresql://postgres:postgres@localhost:5432/vialis`.
Config is env-driven — see `internal/config/config.go` and the README for the
full list of `DATABASE_*`, `HTTP_*`, and `SIMULATION_*` variables. Both `cmd/`
binaries read the same `config.FromEnv()`, and `simulation-test` accepts
`-database-url` to override it.

## Architecture

### Layering

```
cmd/api, cmd/simulation-test        entry points; wire dependencies, no logic
internal/httpapi                    HTTP handlers (currently just /health)
internal/simulation                 orchestrator: Service.Simulate()
internal/simulation/{demand,traveltime,revenue}   estimators (pure domain logic)
internal/simulation/route           shared Route/Position/LineString model + Validate()
internal/database/postgres          repositories: DB-backed implementations of
                                     each estimator's Repository interface
internal/config                     env parsing, defaults, policy construction
sql/                                DDL and ETL scripts (GTFS import, trip data, tariffs)
```

`internal/simulation.Service` is the only orchestrator. It calls, in order:
`route.Validate` → `DemandEstimator.Estimate` → `TravelTimeEstimator.Estimate`
→ `RevenueEstimator.Estimate` (revenue depends on both demand and travel-time
results, since revenue = demand × distance-based tariff). Each estimator is
defined by an interface in `internal/simulation/service.go` and depends only
on a `Repository` interface declared in its own package (`demand.Repository`,
`traveltime.Repository`, `revenue.Repository`) — never on the concrete
`postgres` package. `internal/database/postgres` implements those interfaces
against pgx; that's the only place SQL lives in Go code. This keeps domain
logic testable without a database (see the `*_test.go` files next to each
service) while integration tests in `internal/database/postgres` verify the
SQL against a real PostGIS+H3 instance.

### Domain flow (see docs/arquitectura_motor.md for full detail)

1. **Validation** (`internal/simulation/route`): route needs ≥2 uniquely-ID'd
   stops with valid lat/lon; every stop but the last needs a `PathToNext`
   GeoJSON `LineString` whose endpoints match the current/next stop within 20m
   and whose direction isn't reversed. Nothing downstream runs on invalid
   input — no silent correction.
2. **Demand** (`internal/simulation/demand`): stops are matched to nearby H3
   resolution-8 cells (within `config.SimulationAccessRadiusMeters` = 800m);
   each cell is assigned exclusively to its closest stop (ties broken by stop
   order, then stop ID) to avoid double-counting. An `AccessibilityCalculator`
   (`linear` or `quadratic`, chosen via `SIMULATION_ACCESSIBILITY_METHOD`)
   converts distance into a 0–1 weight. Gross demand comes from the
   origin-destination trip matrix between assigned cells for every stop pair
   `i < j` in route order; potential demand multiplies it by both stops'
   accessibility.
3. **Travel time** (`internal/simulation/traveltime`): each `PathToNext`
   segment is measured geodesically, then matched against existing GTFS
   routes within progressively wider corridors (100m → 300m → 800m, first one
   with ≥3 routes wins; falls back to a global median if no local reference
   exists). Produces off-peak/typical/peak (p25/p50/p75) seconds per segment,
   a source tag, and a confidence level; total confidence is the worst
   segment confidence.
4. **Revenue** (`internal/simulation/revenue`): for each demand stop pair,
   sums segment distances to look up a jurisdiction-specific tariff band
   (`route.Jurisdiction`: `caba`/`province`/`national`), then applies
   `CaptureFactor` and `RegisteredCardShare` policy knobs
   (`SIMULATION_REVENUE_CAPTURE_FACTOR`, `SIMULATION_REGISTERED_CARD_SHARE`)
   to turn potential demand into potential revenue.

### Determinism

Cell assignment, canonical-trip selection, and route consolidation all use
explicit, documented tie-breaking rules (closest stop → earlier stop order →
lower ID; most stops → longest duration → lowest trip ID, etc.) specifically
so results are reproducible. Preserve these when touching that code.

### SQL / data pipeline

`sql/` is organized by data domain, each with its own load order documented in
a README:
- `sql/viajes/` — mobility survey data → PostGIS points → H3 cells →
  origin-destination matrix (`vialis.viajes`, `vialis.hexagonos_viajes`,
  `vialis.matriz_origen_destino`).
- `sql/recorridos/` — GTFS feed → raw staging tables (`gtfs_*_raw`) →
  canonical trip selection → `vialis.recorridos` / `vialis.paradas` /
  `vialis.recorridos_paradas`, including per-segment p25/p50/p75 commercial
  time.
- `sql/tarifas/` — tariff bands by jurisdiction and distance
  (`vialis.tarifas_colectivo`).
- `sql/ddl.sql` — final table definitions; `sql/init_db.sql` bootstraps a new
  database.

Data preparation is an external, administered process — it does not run
inside a simulation request. When changing repository queries, keep in mind
the pipeline order: re-running `transformar_gtfs.sql` replaces
`recorridos`/`paradas`/`recorridos_paradas` but never touches `viajes` or the
OD matrix, and vice versa.
