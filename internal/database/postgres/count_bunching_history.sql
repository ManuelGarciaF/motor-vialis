-- $1: mínimo de muestras; $2: to; $3: from; $4: estado (all/open/closed).
-- $5: duración mínima (s); $6: velocidad máxima mínima por unidad (m/s).
-- Mantener estos filtros idénticos a find_bunching_history.sql.
SELECT count(*)
FROM vialis.aglutinaciones_colectivos
WHERE observaciones >= $1
  AND EXTRACT(EPOCH FROM (ultimo_observado_en - primero_observado_en)) >= $5::double precision
  AND velocidad_maxima_a_metros_segundo >= $6
  AND velocidad_maxima_b_metros_segundo >= $6
  AND primero_observado_en < $2
  AND COALESCE(cerrado_en, ultimo_observado_en) >= $3
  AND (
      $4 = 'all'
      OR ($4 = 'open' AND cerrado_en IS NULL)
      OR ($4 = 'closed' AND cerrado_en IS NOT NULL)
  );
