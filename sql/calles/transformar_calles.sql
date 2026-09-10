\set ON_ERROR_STOP on

-- Variables requeridas por psql:
-- fuente_url, fecha_descarga, fecha_datos, checksum_sha256,
-- alcance_geojson, version_herramienta.

BEGIN;

CREATE TEMP TABLE calles_candidatas ON COMMIT DROP AS
WITH raw AS (
    SELECT
        r.id,
        r.osm_id,
        COALESCE(NULLIF(w.tags -> 'name', ''), r.name) AS nombre,
        w.tags -> 'highway' AS tipo,
        CASE WHEN w.tags -> 'oneway' = '-1' THEN r.target ELSE r.source END AS origen,
        CASE WHEN w.tags -> 'oneway' = '-1' THEN r.source ELSE r.target END AS destino,
        CASE WHEN w.tags -> 'oneway' = '-1' THEN ST_Reverse(r.geom) ELSE r.geom END AS geom,
        w.tags
    FROM vialis.calles_ways_raw r
    JOIN vialis.osm_ways w ON w.osm_id = r.osm_id
    WHERE w.tags -> 'highway' = ANY (ARRAY[
        'motorway', 'trunk', 'primary', 'secondary', 'tertiary',
        'unclassified', 'residential', 'living_street',
        'motorway_link', 'trunk_link', 'primary_link',
        'secondary_link', 'tertiary_link'
    ])
      AND NOT (
          COALESCE(w.tags -> 'access', '') IN ('no', 'private')
          AND COALESCE(w.tags -> 'bus', w.tags -> 'psv', '') NOT IN ('yes', 'designated')
      )
      AND NOT (
          COALESCE(w.tags -> 'motor_vehicle', '') IN ('no', 'private')
          AND COALESCE(w.tags -> 'bus', w.tags -> 'psv', '') NOT IN ('yes', 'designated')
      )
)
SELECT
    id,
    osm_id,
    nombre,
    tipo,
    origen,
    destino,
    ST_Length(geom::geography) AS costo,
    CASE
        WHEN tags -> 'oneway' IN ('yes', '1', 'true', '-1')
          OR tags -> 'junction' = 'roundabout'
        THEN -1::double precision
        ELSE ST_Length(geom::geography)
    END AS costo_inverso,
    tags -> 'oneway' AS sentido_osm,
    tags -> 'access' AS acceso_osm,
    tags -> 'motor_vehicle' AS acceso_vehiculo_motor_osm,
    tags -> 'junction' AS junction_osm,
    tags -> 'bridge' AS bridge_osm,
    tags -> 'tunnel' AS tunnel_osm,
    CASE
        WHEN COALESCE(tags -> 'layer', '0') ~ '^-?[0-9]+$'
        THEN COALESCE((tags -> 'layer')::smallint, 0::smallint)
        ELSE 0::smallint
    END AS layer_osm,
    geom
FROM raw
WHERE origen IS NOT NULL
  AND destino IS NOT NULL
  AND origen <> destino
  AND geom IS NOT NULL
  AND ST_IsValid(geom)
  AND NOT ST_IsEmpty(geom)
  AND ST_NPoints(geom) >= 2
  AND ST_Length(geom::geography) > 0;

CREATE INDEX calles_candidatas_origen_idx ON calles_candidatas (origen);
CREATE INDEX calles_candidatas_destino_idx ON calles_candidatas (destino);
ANALYZE calles_candidatas;

CREATE TEMP TABLE componente_principal ON COMMIT DROP AS
WITH componentes AS (
    SELECT component, node
    FROM pgr_connectedComponents(
        'SELECT id, origen AS source, destino AS target, costo AS cost, costo_inverso AS reverse_cost FROM calles_candidatas'
    )
), principal AS (
    SELECT component
    FROM componentes
    GROUP BY component
    ORDER BY count(*) DESC, component
    LIMIT 1
)
SELECT c.node
FROM componentes c
JOIN principal p USING (component);

CREATE UNIQUE INDEX componente_principal_node_idx ON componente_principal (node);

TRUNCATE TABLE vialis.calles, vialis.calles_vertices RESTART IDENTITY;

INSERT INTO vialis.calles_vertices (id_vertice, osm_node_id, geom)
OVERRIDING SYSTEM VALUE
SELECT v.id, v.osm_id, v.geom
FROM vialis.calles_ways_raw_vertices_pgr v
JOIN componente_principal c ON c.node = v.id
ORDER BY v.id;

INSERT INTO vialis.calles (
    osm_way_id, nombre, tipo, origen, destino, costo, costo_inverso,
    sentido_osm, acceso_osm, acceso_vehiculo_motor_osm, junction_osm,
    bridge_osm, tunnel_osm, layer_osm, geom
)
SELECT
    c.osm_id, c.nombre, c.tipo, c.origen, c.destino, c.costo, c.costo_inverso,
    c.sentido_osm, c.acceso_osm, c.acceso_vehiculo_motor_osm, c.junction_osm,
    c.bridge_osm, c.tunnel_osm, c.layer_osm, c.geom
FROM calles_candidatas c
JOIN componente_principal o ON o.node = c.origen
JOIN componente_principal d ON d.node = c.destino
ORDER BY c.id;

UPDATE vialis.calles_metadata SET activa = FALSE WHERE activa;

INSERT INTO vialis.calles_metadata (
    fuente_url, fecha_descarga, fecha_datos, checksum_sha256, alcance,
    herramienta_importacion, version_herramienta,
    cantidad_aristas, cantidad_vertices, proporcion_descartada, activa
)
SELECT
    :'fuente_url', :'fecha_descarga'::timestamptz, :'fecha_datos'::timestamptz,
    :'checksum_sha256',
    ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON(:'alcance_geojson'), 4326)),
    'osm2pgrouting', :'version_herramienta',
    (SELECT count(*) FROM vialis.calles),
    (SELECT count(*) FROM vialis.calles_vertices),
    1 - (SELECT count(*)::double precision FROM vialis.calles)
        / NULLIF((SELECT count(*) FROM calles_candidatas), 0),
    TRUE;

COMMIT;
