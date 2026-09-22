// Package sql embeds the pipeline scripts so the initializer in
// internal/database/bootstrap can run them without depending on the working
// directory of whoever starts it. The .sql files stay exactly where the READMEs
// of each data domain document them.
//
// Only the scripts that build a database from nothing are embedded. Three kinds
// of file are deliberately left out:
//
//   - every migrar_*.sql, because they upgrade a database that already exists;
//     running them on a fresh one is wrong at best.
//   - recorridos/exportar_ruta_simulacion.sql, a query tool, not a pipeline step.
//   - etapas/importar_etapas.sql, which is empty.
package sql

import _ "embed"

// InitDB creates the vialis schema and enables PostGIS and H3.
//
//go:embed init_db.sql
var InitDB string

// DDL creates the final tables the engine reads.
//
//go:embed ddl.sql
var DDL string

// CrearGTFSRaw recreates the GTFS staging tables (vialis.gtfs_*_raw).
//
//go:embed recorridos/crear_gtfs_raw.sql
var CrearGTFSRaw string

// TransformarGTFS turns the staging tables into recorridos, paradas and
// recorridos_paradas.
//
//go:embed recorridos/transformar_gtfs.sql
var TransformarGTFS string

// TransformarCalles publishes the road graph imported by osm2pgrouting.
//
//go:embed calles/transformar_calles.sql
var TransformarCalles string

// CallesMapConfig configures which OSM ways osm2pgrouting imports.
//
//go:embed calles/mapconfig.xml
var CallesMapConfig string

// CallesScope is the AMBA plus 10 km boundary used by the canonical extract.
//
//go:embed calles/amba-margen-10km.geojson
var CallesScope string

// CrearViajesRaw creates the staging table for the trip survey CSV.
//
//go:embed viajes/crear_viajes_raw.sql
var CrearViajesRaw string

// TransformarViajes turns viajes_raw into vialis.viajes: PostGIS points,
// spatial indexes and H3 cells.
//
//go:embed viajes/transformar_viajes.sql
var TransformarViajes string

// HexagonosViajes computes the point of maximum concurrence of every H3 cell.
//
//go:embed viajes/hexagonos_viajes.sql
var HexagonosViajes string

// MatrizOrigenDestino aggregates expansion factors by origin-destination cell
// pair.
//
//go:embed viajes/matriz_origen_destino.sql
var MatrizOrigenDestino string

// ConexionesRecorridos records which pairs of recorridos a passenger can
// transfer between, and at which pair of stops.
//
//go:embed recorridos/conexiones_recorridos.sql
var ConexionesRecorridos string

// CombinacionesOD aggregates the trips that needed a transfer by
// origin-destination cell pair and hour.
//
//go:embed viajes/combinaciones_od.sql
var CombinacionesOD string

// CombinacionesLineas ranks the pairs of lines that could have served those
// transfers, and records the largest flows behind each pair. It is the only
// script that joins the SUBE survey with the GTFS feed.
//
//go:embed viajes/combinaciones_lineas.sql
var CombinacionesLineas string

// InsertarTarifasVigentes loads the current tariff bands by jurisdiction.
//
//go:embed tarifas/insertar_tarifas_vigentes.sql
var InsertarTarifasVigentes string
