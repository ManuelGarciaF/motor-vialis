-- Ranking de combinaciones de lineas, con su punto de trasbordo y sus tres
-- flujos mas grandes.
--
-- $1 banda horaria (0..23), o NULL para el dia entero
-- $2 cantidad de filas
-- $3 desplazamiento
--
-- Es una lectura barata a proposito: todo el trabajo —el reparto de cada flujo
-- entre sus combinaciones factibles, el descarte de los pares que una sola
-- linea ya cubre, el conteo de alternativas— ya ocurrio en
-- sql/viajes/combinaciones_lineas.sql. Aca solo se ordena y se pagina sobre un
-- agregado que ya existe.
WITH ranking AS (
    SELECT
        combinacion.id_recorrido_primero,
        combinacion.id_recorrido_segundo,
        combinacion.viajes_estimados,
        combinacion.alternativas_promedio,
        combinacion.rango_horario_pico,
        combinacion.h3_origen_dominante,
        combinacion.h3_destino_dominante,
        combinacion.nombre_origen,
        combinacion.nombre_destino,
        combinacion.pares_od_distintos,
        -- Las ventanas se evaluan antes del LIMIT, asi que las dos describen
        -- el ranking completo y no la pagina. El total es lo que el paginador
        -- necesita; el maximo es contra lo que la interfaz mide la gravedad de
        -- cada fila, y tiene que ser el del ranking entero o la pagina 2 se
        -- pintaria contra su propio primero.
        COUNT(*) OVER ()               AS total,
        MAX(combinacion.viajes_estimados) OVER () AS viajes_estimados_maximo
    FROM vialis.combinaciones_lineas AS combinacion
    -- IS NOT DISTINCT FROM y no "=" porque NULL es un valor con significado
    -- aca: es la fila del dia entero, no un dato faltante.
    WHERE combinacion.rango_horario IS NOT DISTINCT FROM $1
    ORDER BY
        combinacion.viajes_estimados DESC,
        combinacion.id_recorrido_primero,
        combinacion.id_recorrido_segundo
    LIMIT $2
    OFFSET $3
)
SELECT
    ranking.total,
    ranking.viajes_estimados_maximo,

    primero.id_recorrido,
    primero.linea,
    primero.ramal,
    primero.nombre_publico,
    primero.direction_id,

    segundo.id_recorrido,
    segundo.linea,
    segundo.ramal,
    segundo.nombre_publico,
    segundo.direction_id,

    ranking.viajes_estimados,
    -- El cast es explicito porque la columna es REAL y el destino en Go es
    -- float64: dejar que el driver elija el plan de escaneo entre float4 y
    -- float64 es una dependencia innecesaria de su version.
    ranking.alternativas_promedio::DOUBLE PRECISION,
    ranking.rango_horario_pico,

    parada_bajada.nombre,
    parada_subida.nombre,
    conexion.distancia_caminata_metros,
    ST_X(parada_subida.posicion),
    ST_Y(parada_subida.posicion),

    ranking.pares_od_distintos,

    -- Las dos zonas que la combinacion une. Es la celda dominante de cada lado
    -- y no el promedio de las coordenadas: un promedio puede caer donde no
    -- viaja nadie. El punto es el de maxima concurrencia de esa celda, que es
    -- donde la gente realmente empieza o termina viajes.
    ranking.h3_origen_dominante::text,
    ranking.nombre_origen,
    ST_X(hexagono_origen.punto_maxima_concurrencia),
    ST_Y(hexagono_origen.punto_maxima_concurrencia),

    ranking.h3_destino_dominante::text,
    ranking.nombre_destino,
    ST_X(hexagono_destino.punto_maxima_concurrencia),
    ST_Y(hexagono_destino.punto_maxima_concurrencia)
FROM ranking
JOIN vialis.recorridos AS primero
  ON primero.id_recorrido = ranking.id_recorrido_primero
JOIN vialis.recorridos AS segundo
  ON segundo.id_recorrido = ranking.id_recorrido_segundo
JOIN vialis.conexiones_recorridos AS conexion
  ON conexion.id_recorrido_origen  = ranking.id_recorrido_primero
 AND conexion.id_recorrido_destino = ranking.id_recorrido_segundo
JOIN vialis.paradas AS parada_bajada
  ON parada_bajada.id_parada = conexion.id_parada_bajada
JOIN vialis.paradas AS parada_subida
  ON parada_subida.id_parada = conexion.id_parada_subida
JOIN vialis.hexagonos_viajes AS hexagono_origen
  ON hexagono_origen.indice_h3 = ranking.h3_origen_dominante
JOIN vialis.hexagonos_viajes AS hexagono_destino
  ON hexagono_destino.indice_h3 = ranking.h3_destino_dominante
ORDER BY
    ranking.viajes_estimados DESC,
    ranking.id_recorrido_primero,
    ranking.id_recorrido_segundo;
