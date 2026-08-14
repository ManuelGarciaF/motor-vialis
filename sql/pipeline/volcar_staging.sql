-- Vuelca la tabla temporal `staging_csv` en una tabla raw del esquema vialis.
--
-- `staging_csv` la crea `cargar_csv.sh` a partir del encabezado real del
-- archivo, con todas sus columnas como TEXT. Este script empareja columnas
-- **por nombre**, no por posición: si el archivo trae columnas de más se
-- ignoran, y si trae de menos quedan en NULL. Eso evita que un feed GTFS que
-- reordene o agregue campos corrompa silenciosamente la carga, que es el
-- riesgo del `\copy` posicional.
--
-- La tabla destino llega en una opción de configuración de la sesión y no como
-- variable de psql, porque psql no interpola variables dentro de cadenas entre
-- signos de dólar:
--     SET vialis.tabla_destino = 'vialis.gtfs_stops_raw';
\set ON_ERROR_STOP on

DO $$
DECLARE
    destino     REGCLASS := current_setting('vialis.tabla_destino')::REGCLASS;
    columnas    TEXT;
    expresiones TEXT;
    filas       BIGINT;
BEGIN
    SELECT
        string_agg(quote_ident(col.attname), ', ' ORDER BY col.attnum),
        -- El staging es todo TEXT. En CSV un campo vacío sin comillas ya llega
        -- como NULL, pero uno entrecomillado llega como cadena vacía: NULLIF
        -- unifica ambos casos antes de convertir al tipo real de la columna.
        string_agg(
            format(
                'NULLIF(BTRIM(%I), '''')::%s',
                col.attname,
                format_type(col.atttypid, col.atttypmod)
            ),
            ', ' ORDER BY col.attnum
        )
    INTO columnas, expresiones
    FROM pg_attribute col
    WHERE col.attrelid = destino
        AND col.attnum > 0
        AND NOT col.attisdropped
        AND EXISTS (
            SELECT 1
            FROM pg_attribute archivo
            WHERE archivo.attrelid = 'pg_temp.staging_csv'::REGCLASS
                AND archivo.attnum > 0
                AND NOT archivo.attisdropped
                AND archivo.attname = col.attname
        );

    IF columnas IS NULL THEN
        RAISE EXCEPTION
            'El encabezado del archivo no comparte ninguna columna con %',
            destino;
    END IF;

    EXECUTE format(
        'INSERT INTO %s (%s) SELECT %s FROM pg_temp.staging_csv',
        destino, columnas, expresiones
    );

    GET DIAGNOSTICS filas = ROW_COUNT;
    RAISE NOTICE 'Cargadas % filas en % (columnas: %)',
        filas, destino, columnas;
END
$$;
