\set ON_ERROR_STOP on

-- Puebla el ranking de combinaciones de lineas y sus principales flujos.
--
-- Es el primer script del pipeline que cruza los dos dominios de datos: la
-- encuesta SUBE (vialis.combinaciones_od) con el feed GTFS
-- (vialis.conexiones_recorridos). Por eso corre al final, cuando los dos estan
-- poblados.
--
-- EL REPARTO
--
-- La fuente no dice que lineas uso la gente. Cada flujo O-D se reparte en
-- partes iguales entre sus combinaciones factibles. Es un supuesto declarado, y
-- por eso se guarda tambien de cuantas alternativas salio cada numero.
--
-- RADIO DE ACCESO
--
-- 400 metros entre el punto de maxima concurrencia de una celda y una parada.
-- Deliberadamente la mitad de los 800 del modelo de demanda, porque la pregunta
-- es otra: aquel radio pregunta si alguien podria caminar hasta esa parada,
-- este pregunta que linea tomo. Con 800 metros el AMBA devuelve una docena de
-- lineas por punta y el producto da 145 combinaciones factibles por par de
-- celdas, medido sobre el dataset completo: repartir un flujo entre 145
-- candidatos no atribuye nada. Cuatrocientos metros son cinco cuadras, que es
-- lo que alguien camina para tomarse un colectivo en particular.
--
-- No confundir con los 300 metros de caminata de trasbordo, que son de
-- conexiones_recorridos.sql y responden una tercera pregunta.
--
-- TECHO DE ALTERNATIVAS
--
-- Un flujo con mas de 10 combinaciones factibles no aporta a ninguna. La red le
-- deja tantas opciones que atribuir sus viajes a un par seria presentar un
-- reparto como una observacion. El ranking queda asi sobre los flujos que la
-- red fuerza o casi, que es de lo unico que se puede afirmar algo, y de paso
-- acota el trabajo: sin el techo la atribucion son mas de cien millones de
-- filas.
--
-- La consecuencia hay que tenerla presente: la suma de `viajes_estimados` no
-- reconstruye el total de viajes con trasbordo de la ciudad, porque los flujos
-- por encima del techo quedan afuera a proposito.
--
-- Puede volver a ejecutarse: vacia las dos tablas antes de recalcularlas.

BEGIN;

TRUNCATE vialis.combinaciones_lineas_flujos;
TRUNCATE vialis.combinaciones_lineas;

-- 1. Que recorridos sirven cada celda que aparece en algun flujo, y entre que
-- posiciones. Del lado del origen interesa la parada mas temprana y del lado
-- del destino la mas tardia, que son las que dejan mas recorrido por delante y
-- por detras para que el trasbordo caiga en el medio.
CREATE TEMP TABLE servicio_celda ON COMMIT DROP AS
WITH celdas AS (
    SELECT h3_origen AS indice_h3 FROM vialis.combinaciones_od
    UNION
    SELECT h3_destino FROM vialis.combinaciones_od
)
SELECT
    celdas.indice_h3,
    recorrido_parada.id_recorrido,
    MIN(recorrido_parada.nro_parada) AS primera_parada,
    MAX(recorrido_parada.nro_parada) AS ultima_parada
FROM celdas
JOIN vialis.hexagonos_viajes AS hexagono
  ON hexagono.indice_h3 = celdas.indice_h3
JOIN vialis.paradas AS parada
  ON ST_DWithin(
         parada.posicion::geography,
         hexagono.punto_maxima_concurrencia::geography,
         400
     )
JOIN vialis.recorridos_paradas AS recorrido_parada
  ON recorrido_parada.id_parada = parada.id_parada
GROUP BY celdas.indice_h3, recorrido_parada.id_recorrido;

CREATE INDEX idx_servicio_celda ON servicio_celda (indice_h3, id_recorrido);
ANALYZE servicio_celda;

-- Los pares de celdas son los mismos para las 24 horas, asi que todo lo
-- geometrico se calcula una vez sobre el par y recien al final se cruza con las
-- bandas horarias.
CREATE TEMP TABLE pares_celdas ON COMMIT DROP AS
SELECT DISTINCT h3_origen, h3_destino FROM vialis.combinaciones_od;

CREATE INDEX idx_pares_celdas ON pares_celdas (h3_origen, h3_destino);
ANALYZE pares_celdas;

-- Nombre de cada celda: la parada mas cercana a su punto de maxima
-- concurrencia. Se resuelve una vez por celda y no una vez por fila del
-- resultado. Sobre el dataset completo son 4.744 celdas distintas contra
-- ~450.000 extremos de fila: resolverlo abajo, en el INSERT, multiplicaba por
-- noventa y cinco las busquedas KNN y el paso pasaba de segundos a no terminar.
--
-- El desempate por id_parada es deterministico: dos corridas sobre los mismos
-- datos tienen que nombrar la celda igual.
CREATE TEMP TABLE nombre_celda ON COMMIT DROP AS
WITH celdas_usadas AS (
    SELECT h3_origen AS indice_h3 FROM pares_celdas
    UNION
    SELECT h3_destino FROM pares_celdas
)
SELECT
    celdas_usadas.indice_h3,
    (
        SELECT parada.nombre
        FROM vialis.paradas AS parada
        ORDER BY
            parada.posicion <-> hexagono.punto_maxima_concurrencia,
            parada.id_parada
        LIMIT 1
    ) AS nombre
FROM celdas_usadas
JOIN vialis.hexagonos_viajes AS hexagono
  ON hexagono.indice_h3 = celdas_usadas.indice_h3;

CREATE UNIQUE INDEX idx_nombre_celda ON nombre_celda (indice_h3);
ANALYZE nombre_celda;

-- 2. Pares de celdas que una sola linea ya cubre de punta a punta, en el
-- sentido correcto. Ahi el trasbordo no era obligatorio, y el flujo no habla de
-- un hueco de la red.
CREATE TEMP TABLE pares_directos ON COMMIT DROP AS
SELECT DISTINCT pares_celdas.h3_origen, pares_celdas.h3_destino
FROM pares_celdas
JOIN servicio_celda AS origen
  ON origen.indice_h3 = pares_celdas.h3_origen
JOIN servicio_celda AS destino
  ON destino.indice_h3 = pares_celdas.h3_destino
 AND destino.id_recorrido = origen.id_recorrido
WHERE destino.ultima_parada > origen.primera_parada;

CREATE INDEX idx_pares_directos ON pares_directos (h3_origen, h3_destino);
ANALYZE pares_directos;

-- 3. Combinaciones factibles de dos colectivos por par de celdas. Factible
-- exige que el par de recorridos tenga punto de trasbordo y que ese punto caiga
-- en orden: despues de donde se sube al primero y antes de donde se baja del
-- segundo. Los EXISTS miran nro_parada en vez de unir, porque un recorrido
-- circular pasa dos veces por la misma parada y la union repetiria la
-- combinacion una vez por pasada.
CREATE TEMP TABLE itinerarios ON COMMIT DROP AS
SELECT
    pares_celdas.h3_origen,
    pares_celdas.h3_destino,
    origen.id_recorrido  AS id_recorrido_primero,
    destino.id_recorrido AS id_recorrido_segundo
FROM pares_celdas
JOIN servicio_celda AS origen
  ON origen.indice_h3 = pares_celdas.h3_origen
JOIN servicio_celda AS destino
  ON destino.indice_h3 = pares_celdas.h3_destino
JOIN vialis.conexiones_recorridos AS conexion
  ON conexion.id_recorrido_origen  = origen.id_recorrido
 AND conexion.id_recorrido_destino = destino.id_recorrido
WHERE NOT EXISTS (
    SELECT 1 FROM pares_directos
    WHERE pares_directos.h3_origen  = pares_celdas.h3_origen
      AND pares_directos.h3_destino = pares_celdas.h3_destino
)
AND EXISTS (
    SELECT 1
    FROM vialis.recorridos_paradas AS bajada
    WHERE bajada.id_recorrido = origen.id_recorrido
      AND bajada.id_parada    = conexion.id_parada_bajada
      AND bajada.nro_parada   > origen.primera_parada
)
AND EXISTS (
    SELECT 1
    FROM vialis.recorridos_paradas AS subida
    WHERE subida.id_recorrido = destino.id_recorrido
      AND subida.id_parada    = conexion.id_parada_subida
      AND subida.nro_parada   < destino.ultima_parada
);

CREATE INDEX idx_itinerarios ON itinerarios (h3_origen, h3_destino);
ANALYZE itinerarios;

-- 4. Cuantas combinaciones factibles tiene cada par de celdas. Este numero es
-- el divisor del reparto y tambien la medida de cuanto vale la afirmacion: con
-- una alternativa es un hecho, con diez es el limite de lo que este agregado
-- esta dispuesto a afirmar, y por encima de diez el par no entra.
--
-- El HAVING es lo que hace el descarte, y es el mismo JOIN de mas abajo el que
-- lo aplica: un par que no esta aca no aparece en `atribucion`.
CREATE TEMP TABLE alternativas_por_par ON COMMIT DROP AS
SELECT h3_origen, h3_destino, COUNT(*)::INTEGER AS alternativas
FROM itinerarios
GROUP BY h3_origen, h3_destino
HAVING COUNT(*) <= 10;

CREATE INDEX idx_alternativas_por_par
ON alternativas_por_par (h3_origen, h3_destino);
ANALYZE alternativas_por_par;

-- 5. El reparto propiamente dicho, por combinacion, par de celdas y hora.
CREATE TEMP TABLE atribucion ON COMMIT DROP AS
SELECT
    itinerarios.id_recorrido_primero,
    itinerarios.id_recorrido_segundo,
    combinacion.rango_horario,
    combinacion.h3_origen,
    combinacion.h3_destino,
    combinacion.viajes_estimados / alternativas_por_par.alternativas
        AS viajes_atribuidos,
    alternativas_por_par.alternativas
FROM vialis.combinaciones_od AS combinacion
JOIN itinerarios
  ON itinerarios.h3_origen  = combinacion.h3_origen
 AND itinerarios.h3_destino = combinacion.h3_destino
JOIN alternativas_por_par
  ON alternativas_por_par.h3_origen  = combinacion.h3_origen
 AND alternativas_por_par.h3_destino = combinacion.h3_destino;

ANALYZE atribucion;

-- 6. Ranking por banda horaria. El promedio de alternativas se pondera por
-- volumen: lo que importa no es cuantos flujos aportaron sino cuanta gente.
-- El GREATEST protege el CHECK de la tabla contra el redondeo a REAL.
INSERT INTO vialis.combinaciones_lineas (
    id_recorrido_primero,
    id_recorrido_segundo,
    rango_horario,
    viajes_estimados,
    alternativas_promedio,
    rango_horario_pico
)
SELECT
    id_recorrido_primero,
    id_recorrido_segundo,
    rango_horario,
    SUM(viajes_atribuidos),
    GREATEST(
        SUM(viajes_atribuidos * alternativas)
            / NULLIF(SUM(viajes_atribuidos), 0),
        1
    )::REAL,
    rango_horario
FROM atribucion
GROUP BY id_recorrido_primero, id_recorrido_segundo, rango_horario;

-- 7. La fila del dia entero se deriva de las 24 anteriores en vez de recorrer
-- la atribucion otra vez. La hora pico es la de mayor volumen, con desempate
-- por hora para que dos corridas den lo mismo.
INSERT INTO vialis.combinaciones_lineas (
    id_recorrido_primero,
    id_recorrido_segundo,
    rango_horario,
    viajes_estimados,
    alternativas_promedio,
    rango_horario_pico
)
SELECT
    id_recorrido_primero,
    id_recorrido_segundo,
    NULL,
    SUM(viajes_estimados),
    GREATEST(
        SUM(viajes_estimados * alternativas_promedio)
            / NULLIF(SUM(viajes_estimados), 0),
        1
    )::REAL,
    (ARRAY_AGG(
        rango_horario ORDER BY viajes_estimados DESC, rango_horario
    ))[1]
FROM vialis.combinaciones_lineas
WHERE rango_horario IS NOT NULL
GROUP BY id_recorrido_primero, id_recorrido_segundo;

-- 8. Los tres viajes mas grandes de cada combinacion, para el detalle de una
-- fila del ranking.
--
-- Se agrupa por par de celdas y NO por par y hora. Un viaje es su par de
-- celdas: la hora es un atributo suyo, no otra fila. Agrupando por las dos
-- cosas, el mismo viaje a las 5 y a las 10 ocupaba dos de los tres lugares y
-- se leia como dos viajes distintos que nadie podia diferenciar, porque tenian
-- el mismo origen y el mismo destino.
--
-- `alternativas` es una propiedad del par de celdas, igual para las 24 horas,
-- asi que MIN devuelve ese valor y no un resumen de varios.
INSERT INTO vialis.combinaciones_lineas_flujos (
    id_recorrido_primero,
    id_recorrido_segundo,
    posicion,
    h3_origen,
    h3_destino,
    rango_horario_pico,
    viajes_estimados,
    alternativas,
    nombre_origen,
    nombre_destino
)
SELECT
    ordenados.id_recorrido_primero,
    ordenados.id_recorrido_segundo,
    ordenados.posicion,
    ordenados.h3_origen,
    ordenados.h3_destino,
    ordenados.rango_horario_pico,
    ordenados.viajes_atribuidos,
    ordenados.alternativas,
    parada_origen.nombre,
    parada_destino.nombre
-- El recorte a los tres primeros va en su propio nivel para que el ranking se
-- resuelva una sola vez, y los nombres salen de un join plano contra
-- nombre_celda en vez de una busqueda por fila.
FROM (
  SELECT * FROM (
    SELECT
        id_recorrido_primero,
        id_recorrido_segundo,
        h3_origen,
        h3_destino,
        SUM(viajes_atribuidos) AS viajes_atribuidos,
        MIN(alternativas) AS alternativas,
        -- La hora que mas viajes concentra, con desempate por hora para que dos
        -- corridas sobre los mismos datos elijan la misma.
        (ARRAY_AGG(
            rango_horario ORDER BY viajes_atribuidos DESC, rango_horario
        ))[1] AS rango_horario_pico,
        ROW_NUMBER() OVER (
            PARTITION BY id_recorrido_primero, id_recorrido_segundo
            ORDER BY
                SUM(viajes_atribuidos) DESC,
                h3_origen,
                h3_destino
        )::SMALLINT AS posicion
    FROM atribucion
    GROUP BY
        id_recorrido_primero,
        id_recorrido_segundo,
        h3_origen,
        h3_destino
  ) AS rankeados
  WHERE rankeados.posicion <= 3
) AS ordenados
JOIN nombre_celda AS parada_origen
  ON parada_origen.indice_h3 = ordenados.h3_origen
JOIN nombre_celda AS parada_destino
  ON parada_destino.indice_h3 = ordenados.h3_destino;

COMMIT;

VACUUM ANALYZE vialis.combinaciones_lineas;
VACUUM ANALYZE vialis.combinaciones_lineas_flujos;
