\set ON_ERROR_STOP on

-- Staging de las relaciones OSM type=restriction de calles.osm. osm2pgrouting
-- 3.0 no sirve para esto: sólo conserva relaciones cuyas etiquetas figuran en
-- mapconfig.xml, y aun así guarda los miembros sin rol y sin los nodos, que son
-- justamente el via. cmd/initdb las lee con osmium y las copia acá tal como
-- están en OSM; transformar_restricciones.sql decide cuáles aplican.
--
-- miembros conserva el orden de la relación: [{"tipo", "ref", "rol"}, ...],
-- con tipo node, way o relation.
DROP TABLE IF EXISTS vialis.calles_restricciones_raw;

CREATE TABLE vialis.calles_restricciones_raw (
    osm_relation_id            BIGINT PRIMARY KEY,
    etiquetas                  JSONB NOT NULL,
    miembros                   JSONB NOT NULL
);
