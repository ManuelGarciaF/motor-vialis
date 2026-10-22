#!/usr/bin/env python3
"""Read-only spike regression tests; fixture uses session-local TEMP tables only.
Run with DATABASE_URL configured and psql on PATH. No persistent DB writes.
"""
import json
import os
from pathlib import Path
import subprocess

SQL = Path(__file__).with_name("spike-bottlenecks-segments.sql").read_text()
# Redirect all source tables to session-local fixtures; production schema untouched.
SQL = SQL.replace("vialis.", "pg_temp.")
FIXTURE = r"""
CREATE TEMP TABLE recorridos AS
SELECT 'r'::text gtfs_route_id, 0::smallint direction_id, 's'::text gtfs_shape_id,
 ST_Transform(ST_GeomFromText('LINESTRING(370000 6170000,372000 6170000)',32721),4326) geom;
CREATE TEMP TABLE posiciones_colectivos_raw AS
SELECT '2026-10-10'::date dia_observado,
 '2026-10-10T12:00:00Z'::timestamptz + k * interval '60 seconds' observado_en,
 'v'::text id_vehiculo, 'g'::text gcba_route_id, 0::smallint gcba_direction,
 gtfs_route_id, direction_id gtfs_direction_id, gtfs_shape_id,
 md5(encode(ST_AsEWKB(geom),'hex')) gtfs_shape_hash,
 ST_Transform(ST_SetSRID(ST_MakePoint(370000 + k * 500,6170000),32721),4326) posicion
FROM recorridos CROSS JOIN generate_series(0,4) k;
"""

def run(extra="", gap=180):
    result = subprocess.run(
        ["psql", os.environ["DATABASE_URL"], "-X", "-qAt", "-v", "ON_ERROR_STOP=1",
         "-v", "from=2026-10-10T12:00:00Z", "-v", "to=2026-10-10T13:00:00Z",
         "-v", f"max_gap_seconds={gap}"],
        input=FIXTURE + extra + SQL, text=True, capture_output=True, check=True,
    )
    return json.loads(result.stdout)

normal = run()
assert normal["coverage"]["rawPositions"] == 5
assert normal["edges"]["valid"] == 4
for segment in normal["segments"]:
    assert segment["passages"] == {300: 7, 500: 4, 800: 3}[segment["size_m"]], segment
    assert abs(segment["median_speed_kph"] - 30) < 0.01, segment
    assert segment["same_gps_interval"] <= segment["passages"]
assert all(s["passages"] == 0 for s in run(gap=30)["segments"])
assert all(s["passages"] == 0 for s in run(
    "UPDATE posiciones_colectivos_raw SET gtfs_shape_hash='old';\n")["segments"])
assert all(s["passages"] == 0 for s in run(
    "UPDATE posiciones_colectivos_raw SET posicion=ST_Translate(posicion,0,0.1);\n")["segments"])
assert all(s["passages"] == 0 for s in run(
    "UPDATE posiciones_colectivos_raw SET observado_en='2026-10-10T12:04:00Z'::timestamptz - (observado_en-'2026-10-10T12:00:00Z'::timestamptz);\n")["segments"])
assert all(s["passages"] == 0 for s in run(
    "DELETE FROM posiciones_colectivos_raw;\n")["segments"])
print("PASS: boundary counts, merged remainder, uniform speed, gaps, versions, off-shape, backwards, empty input")
