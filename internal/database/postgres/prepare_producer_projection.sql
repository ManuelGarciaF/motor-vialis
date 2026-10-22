-- Transaction-local static geometry only: no GPS archive or persistent DDL.
-- Exact original edges and geodesic progress preserve all matching alternatives.
CREATE TEMP TABLE pg_temp.bottlenecks_projection_edges (
    layout_id bigint NOT NULL,
    edge integer NOT NULL,
    geom geometry(LineString,4326) NOT NULL,
    geog geography(LineString,4326) NOT NULL,
    length_m double precision NOT NULL,
    start_m double precision NOT NULL
) ON COMMIT DROP;
CREATE INDEX bottlenecks_projection_edges_layout
    ON pg_temp.bottlenecks_projection_edges (layout_id);
CREATE INDEX bottlenecks_projection_edges_geog
    ON pg_temp.bottlenecks_projection_edges USING gist (geog);
