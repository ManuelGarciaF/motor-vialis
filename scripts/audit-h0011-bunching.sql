-- Auditoría operacional sin escrituras; ejecutar con psql -X -v ON_ERROR_STOP=1 -f.
-- Snapshot consistente aunque el worker continúe ingiriendo.
\pset pager off
BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
SELECT now() AS auditado_en, count(*) AS capturas,
       min(recibido_en) AS primera, max(recibido_en) AS ultima
FROM vialis.capturas_colectivos;

WITH capturas AS (
 SELECT *, lag(hash_posiciones) OVER (ORDER BY id_captura) AS hash_anterior
 FROM vialis.capturas_colectivos
)
SELECT id_captura, recibido_en, filas_validas, filas_stale,
       filas_no_vinculadas, filas_insertadas, cobertura_parcial,
       aglutinaciones_analizadas, hash_posiciones = hash_anterior AS respuesta_repetida
FROM capturas ORDER BY id_captura DESC LIMIT 20;

SELECT c.id_captura, c.filas_insertadas, count(p.id_captura) AS raw_presentes
FROM vialis.capturas_colectivos c
LEFT JOIN vialis.posiciones_colectivos_raw p USING (id_captura)
WHERE c.recibido_en >= now() - interval '1 day'
GROUP BY c.id_captura, c.filas_insertadas ORDER BY c.id_captura;
-- La igualdad de conteos sólo es exigible si aún no expiraron sus particiones raw.

SELECT count(*) AS total,
 count(*) FILTER (WHERE observaciones >= 2) AS confirmados,
 count(*) FILTER (WHERE observaciones = 1) AS candidatos,
 count(*) FILTER (WHERE cerrado_en IS NULL) AS abiertos
FROM vialis.aglutinaciones_colectivos;
SELECT motivo_cierre, count(*) FROM vialis.aglutinaciones_colectivos GROUP BY 1 ORDER BY 1;

SELECT
 count(*) FILTER (WHERE ultima_distancia_metros > 200 OR distancia_minima_metros > ultima_distancia_metros) AS distancias_invalidas,
 count(*) FILTER (WHERE abs(ST_Distance(ultima_posicion_a::geography, ultima_posicion_b::geography) - ultima_distancia_metros) > 0.001) AS distancia_geografica_inconsistente,
 count(*) FILTER (WHERE observado_a_en IS NULL OR observado_b_en IS NULL OR ultima_confirmacion_en IS NULL) AS timestamps_faltantes,
 count(*) FILTER (WHERE ultimo_observado_en <> greatest(observado_a_en, observado_b_en)) AS timestamp_par_inconsistente,
 count(*) FILTER (WHERE primero_observado_en > ultimo_observado_en OR cerrado_en < ultimo_observado_en) AS orden_temporal_invalido,
 count(*) FILTER (WHERE (cerrado_en IS NULL) <> (motivo_cierre IS NULL)) AS cierre_inconsistente,
 count(*) FILTER (WHERE id_vehiculo_a COLLATE "C" >= id_vehiculo_b COLLATE "C") AS par_no_canonico
FROM vialis.aglutinaciones_colectivos;

-- Ejemplos auditables, no evidencia de anomalía operacional por sí solos.
WITH ejemplos AS (
 SELECT *, row_number() OVER (
  PARTITION BY coalesce(motivo_cierre, 'open') ORDER BY observaciones DESC, id_evento DESC
 ) AS n FROM vialis.aglutinaciones_colectivos WHERE observaciones >= 2
)
SELECT id_evento, linea, ramal, gcba_route_id, gcba_direction,
 id_vehiculo_a, id_vehiculo_b, observaciones, primero_observado_en,
 ultimo_observado_en, cerrado_en, motivo_cierre, distancia_minima_metros,
 ultima_distancia_metros, ST_AsGeoJSON(ultima_posicion_a) AS posicion_a,
 ST_AsGeoJSON(ultima_posicion_b) AS posicion_b
FROM ejemplos WHERE n <= 2 ORDER BY motivo_cierre NULLS FIRST, id_evento;
COMMIT;
