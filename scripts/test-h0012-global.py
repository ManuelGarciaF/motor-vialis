#!/usr/bin/env python3
"""Regression test: audit uses explicit GCBA->GTFS mapping, never ID equality.
Requires DATABASE_URL and psql; only session-local TEMP tables are written.
"""
import json
import os
from pathlib import Path
import subprocess

SQL = Path(__file__).with_name("audit-h0012-global.sql").read_text().replace("vialis.", "pg_temp.")
FIXTURE = r"""
CREATE TEMP TABLE equivalencias_recorridos_gcba(
 gcba_route_id text,gcba_direction smallint,gtfs_route_id text,gtfs_direction_id smallint);
INSERT INTO equivalencias_recorridos_gcba VALUES
 ('api-1',0,'gtfs-a',1),('api-2',0,'gtfs-b',0),('api-3',0,'missing-target',0),
 ('gtfs-b',0,'gtfs-a',1);
CREATE TEMP TABLE recorridos(id_recorrido bigint,gtfs_route_id text,direction_id smallint,
 linea text,ramal text,gtfs_shape_id text,geom geometry(LineString,4326));
INSERT INTO recorridos VALUES
 (1,'gtfs-a',1,'A','x','shape-a',ST_GeomFromText('LINESTRING(-58.4 -34.6,-58.39 -34.6)',4326)),
 (2,'gtfs-b',0,'B','y','shape-b',ST_GeomFromText('LINESTRING(-58.4 -34.62,-58.39 -34.62)',4326));
CREATE TEMP TABLE posiciones_colectivos_raw(dia_observado date,observado_en timestamptz,
 id_vehiculo text,gcba_route_id text,gcba_direction smallint,gtfs_route_id text,
 gtfs_direction_id smallint,gtfs_shape_id text,gtfs_shape_hash text,
 posicion geometry(Point,4326),velocidad_metros_segundo float8);
INSERT INTO posiciones_colectivos_raw
SELECT '2026-10-10','2026-10-10T12:00:00Z'::timestamptz + n * interval '1 minute',
 'v-'||n,api,api_dir,raw_target,raw_dir,coalesce(r.gtfs_shape_id,'missing'),
 CASE WHEN old_geometry THEN 'old-hash' ELSE coalesce(md5(encode(ST_AsEWKB(r.geom),'hex')),'missing') END,
 ST_Point(lon,lat,4326),5
FROM (VALUES
 (1,'api-1',0,'gtfs-a',1,false,-58.395,-34.6),
 (2,'api-1',0,'gtfs-a',1,false,-58.395,-34.62),
 (3,'api-1',0,'gtfs-a',1,false,-59.0,-35.0),
 (4,'api-2',0,'gtfs-a',1,false,-58.395,-34.6),
 (5,'api-1',0,'gtfs-a',1,true,-58.395,-34.6),
 (6,'api-unmapped',0,'gtfs-a',1,false,-58.395,-34.6),
 (7,'api-3',0,'missing-target',0,false,-58.395,-34.6),
 (8,'api-1',1,'gtfs-a',1,false,-58.395,-34.6),
 (9,'gtfs-b',0,'gtfs-a',1,false,-58.395,-34.6),
 (60,'api-1',0,'gtfs-a',1,false,-58.395,-34.6)
) seed(n,api,api_dir,raw_target,raw_dir,old_geometry,lon,lat)
LEFT JOIN recorridos r ON r.gtfs_route_id=raw_target AND r.direction_id=raw_dir;
"""

def run(fixture=FIXTURE, compare_catalog=True):
    result = subprocess.run(
        ["psql", os.environ["DATABASE_URL"], "-X", "-qAt", "-v", "ON_ERROR_STOP=1",
         "-v", "from=2026-10-10T12:00:00Z", "-v", "to=2026-10-10T13:00:00Z",
         "-v", f"compare_catalog={str(compare_catalog).lower()}"],
        input=fixture + SQL, text=True, capture_output=True, check=True,
    )
    return json.loads(result.stdout)

report = run()
assert report["sourceTransactionReadOnly"] is True
s = report["summary"]
assert s["rawPositions"] == 9, s
assert s["comparablePositions"] == 4, s
assert s["withinRadius"] == 2 and s["outsideRadius"] == 2, s
assert s["outsideNearOtherShape"] == 1 and s["outsideNearNoShape"] == 1, s
assert report["activeTargetsMissingCatalog"] == 1
assert {r["status"]: r["positions"] for r in report["statuses"]} == {
    "comparable": 4, "mapping_changed": 1, "geometry_changed": 1,
    "no_active_equivalence": 2, "no_catalog_target": 1,
}
# A GCBA ID happens to equal ANOTHER GTFS ID: only the explicit mapping is valid.
collision = next(p for p in report["providers"] if p["gcba_route_id"] == "gtfs-b")
assert collision["gtfs_route_id"] == "gtfs-a" and collision["within_radius"] == 1
assert collision["gtfs_direction_id"] == 1 and collision["gcba_direction"] == 0
primary = run(compare_catalog=False)
assert primary["catalogComparisonEnabled"] is False
assert primary["summary"]["outsideNearNoShape"] is None
assert primary["summary"]["outsideRadius"] == s["outsideRadius"]
assert primary["summary"]["withinRadius"] == s["withinRadius"]
assert primary["statuses"] == report["statuses"]
# A 301-vertex zigzag requires three chunks. Probe overlap vertices and final
# endpoint, so a dropped edge/chunk boundary cannot silently lose coverage.
zigzag = run(FIXTURE + r"""
INSERT INTO recorridos
SELECT 3,'gtfs-c',0,'C','z','shape-c',ST_MakeLine(
 ST_Point(-58.48+k*0.00002,-34.64+sin(k/8.0)*0.0002,4326) ORDER BY k)
FROM generate_series(0,300) k;
INSERT INTO posiciones_colectivos_raw
SELECT '2026-10-10','2026-10-10T12:20:00Z','z-'||n,'api-1',0,'gtfs-a',1,'shape-a',
 (SELECT md5(encode(ST_AsEWKB(geom),'hex')) FROM recorridos WHERE id_recorrido=1),
 ST_PointN(geom,n),5
FROM recorridos CROSS JOIN (VALUES(126),(127),(128),(254),(255),(301)) points(n)
WHERE id_recorrido=3;
""")
assert zigzag["catalogFragments"] == 5
assert zigzag["summary"]["outsideNearOtherShape"] == 7
assert zigzag["summary"]["outsideNearNoShape"] == 1
integrity = zigzag["fragmentIntegrity"]
assert integrity["originalEdges"] == integrity["fragmentEdges"] == 302
assert abs(integrity["originalLengthMeters"] - integrity["fragmentLengthMeters"]) < 1e-6
empty = run(FIXTURE + "DELETE FROM posiciones_colectivos_raw;\n")
assert empty["summary"]["rawPositions"] == 0 and empty["providers"] == []
assert empty["targets"] == [] and empty["days"] == []
print("PASS: explicit equivalences, direction, colliding IDs, historical mapping/geometry, missing mappings/targets, half-open window, catalog coverage, empty raw, read-only snapshot, fragment boundaries/edge/length integrity")
