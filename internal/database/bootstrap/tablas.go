package bootstrap

// GTFSDirectory is the folder of the GTFS feed inside the data directory.
const GTFSDirectory = "colectivos-gtfs"

// ViajesFileName is the trip survey CSV, at the root of the data directory.
const ViajesFileName = "viajes_BAdata_20241016.csv"

// gtfsFiles maps each GTFS file to its staging table, in the order the feed is
// loaded. The mapping is the one documented in sql/recorridos/README.md and the
// column order is the one declared by sql/recorridos/crear_gtfs_raw.sql.
//
// The order of the files themselves does not matter: the staging tables have no
// foreign keys between them, precisely so the load never has to respect one.
var gtfsFiles = []csvFile{
	{
		FileName: "agency.txt",
		Table:    "vialis.gtfs_agency_raw",
		Columns: text(
			"agency_id",
			"agency_name",
			"agency_url",
			"agency_timezone",
			"agency_lang",
			"agency_phone",
		),
	},
	{
		FileName: "routes.txt",
		Table:    "vialis.gtfs_routes_raw",
		Columns: joinColumns(
			text(
				"route_id",
				"agency_id",
				"route_short_name",
				"route_long_name",
				"route_desc",
			),
			integer("route_type"),
		),
	},
	{
		FileName: "trips.txt",
		Table:    "vialis.gtfs_trips_raw",
		Columns: joinColumns(
			text(
				"route_id",
				"service_id",
				"trip_id",
				"trip_headsign",
				"trip_short_name",
			),
			integer("direction_id"),
			text("block_id", "shape_id"),
			integer("exceptional"),
		),
	},
	{
		FileName: "stops.txt",
		Table:    "vialis.gtfs_stops_raw",
		Columns: joinColumns(
			text("stop_id", "stop_code", "stop_name"),
			decimal("stop_lat", "stop_lon"),
		),
	},
	{
		FileName: "stop_times.txt",
		Table:    "vialis.gtfs_stop_times_raw",
		Columns: joinColumns(
			text("trip_id", "arrival_time", "departure_time", "stop_id"),
			integer("stop_sequence", "timepoint"),
			decimal("shape_dist_traveled"),
		),
	},
	{
		FileName: "shapes.txt",
		Table:    "vialis.gtfs_shapes_raw",
		Columns: joinColumns(
			text("shape_id"),
			decimal("shape_pt_lat", "shape_pt_lon"),
			integer("shape_pt_sequence"),
			decimal("shape_dist_traveled"),
		),
	},
	{
		FileName: "calendar_dates.txt",
		Table:    "vialis.gtfs_calendar_dates_raw",
		Columns: joinColumns(
			text("service_id", "date"),
			integer("exception_type"),
		),
	},
}

// viajesFile maps the trip survey CSV to its staging table, with the column
// order declared by sql/viajes/crear_viajes_raw.sql.
var viajesFile = csvFile{
	FileName: ViajesFileName,
	Table:    "vialis.viajes_raw",
	Columns: joinColumns(
		integer(
			"id_tarjeta",
			"id_viaje",
			"cantidad_etapas",
			"rango_horario",
			"etapas_subte",
			"etapas_tren",
			"etapas_colectivo",
		),
		decimal(
			"longitud_origen_viaje",
			"latitud_origen_viaje",
			"longitud_destino_viaje",
			"latitud_destino_viaje",
		),
		text("departamento_origen_viaje", "departamento_destino_viaje"),
		decimal("factor_expansion_viaje"),
		text("etapas_incompletas", "genero"),
		integer("grupo_edad"),
	),
}
