-- $1: fecha de corte de retención. No elimina capturas aún referenciadas por raw.
DELETE FROM vialis.capturas_colectivos c
WHERE c.recibido_en < $1
  AND NOT EXISTS (
      SELECT 1
      FROM vialis.posiciones_colectivos_raw p
      WHERE p.id_captura = c.id_captura
  );
