# RF05 — Desvíos: funcionamiento y operación

`POST /detours` construye una variante temporal de una ruta para evitar un corte,
selecciona un camino y compara el resultado con la ruta original. No persiste
cortes ni variantes, no reubica/agrega paradas y no decide la vigencia del corte.
El dominio está en `internal/simulation/detour`; el contrato HTTP en
[OpenAPI](openapi.yaml) y la metodología general de simulación en
[arquitectura del motor](arquitectura_motor.md#13-desvíos-por-cortes).

## Funcionamiento actual

1. Valida ruta, corte GeoJSON WGS84 y criterio. El decoder actual admite
   `LineString` o `MultiLineString`: varios ramales pueden representar una barrera
   en T/H, no hace falta unirlos con un tramo ficticio.
2. Bloquea las aristas que tocan el corredor de 5 m del corte, incluso las que
   lo cruzan perpendicularmente. No propaga el bloqueo a toda una vía OSM.
   La interpretación es 2D; cruces a distinto nivel requieren revisión.
3. Clasifica paradas por distancia al corte: hasta 20 m, forzosamente no disponibles;
   hasta 500 m, opcionales; las demás son obligatorias y conservan su orden.
4. Recorta los intervalos afectados sobre un área nominal de 1 km, conserva la
   geometría exterior y proyecta anclas/paradas sobre aristas con puntos virtuales,
   no sobre el vértice más cercano. Los conectores son explícitos y se validan.
5. Obtiene un snapshot TomTom, asocia costos por sentido y resuelve caminos con
   pgRouting. Un DAG de paradas conservadas/omitidas evita enumerar subconjuntos.
6. Reconstruye y valida la variante completa antes de compararla con el simulador.
   El tiempo de decisión con tráfico se informa separado del tiempo comercial GTFS.
   Para tarifas se conserva la distancia comercial original entre paradas retenidas,
   no se cobra el rodeo como mayor distancia al pasajero.

Criterios lexicográficos:

| Criterio | Prioridad | Desempate |
|---|---|---|
| `MENOR_TIEMPO` | Menos segundos | Menos paradas omitidas |
| `MENOR_PARADAS_PERDIDAS` | Menos paradas omitidas | Menos segundos |

No hay penalidad implícita por omitir: `MENOR_TIEMPO` puede saltar todas las
opcionales si ahorra tiempo. Los desempates adicionales son deterministas.
La respuesta incluye `variant`, `uncoveredStops`, `comparison` y `trace` con
aristas, carga del grafo, cobertura y antigüedad del tráfico.

## Tráfico y pgRouting

- TomTom Traffic Flow Vector Tiles `absolute`, **zoom 14**. Se eligió porque
  las muestras z15/z16 aumentaban requests sin mejorar materialmente cobertura.
  Buena cobertura central/arterial no garantiza cobertura de calles residenciales.
- Match directo por costo dirigido: radio de 15 m y tolerancia angular de 45°;
  `one_side` compite sólo para el sentido compatible y `full` para ambos permitidos
  por OSM. No copiar el candidato más próximo indiscriminadamente a ambos sentidos.
- Sin match directo, intenta mediana de 3 a 5 segmentos de la misma categoría
  a hasta 300 m: `tomtom_nearby_estimate`, no observación de esa calle.
- La implementación vigente también usa **`default_fallback`** cuando no hay
  velocidad utilizable: mediana de velocidades válidas del snapshot o, si no hay,
  `config.DetourDefaultSpeedKPH` (20 km/h). `forwardDefaultEdges` y
  `reverseDefaultEdges` exponen esa cobertura. No presentar todo el tiempo como
  observado ni restaurar la exclusión de aristas del antiguo plan sin decidirlo.
- Caché en memoria por tile: TTL 30 min, LRU de 256 entradas/16 MiB, deduplicación
  concurrente, hasta 10 QPS y timeout TomTom de 10 s. No guarda tiles en PostgreSQL.
  Una falla del proveedor sigue siendo un error, no una variante calculada sin aviso.
- Costos, puntos y restricciones viven en tablas TEMP de la misma transacción,
  con `ON COMMIT DROP`; no se modifican costos permanentes de `vialis.calles`.
- `pgr_trsp_withPoints` aplica restricciones OSM node-via del grafo local con
  `details => false`: en pgRouting 4.0, activarlo podía perder restricciones si
  otro punto partía la arista. No eliminar ese ajuste como una simplificación.
- Hay recuperación de pares con vueltas en U artificiales mediante ruteo por par.
  El servicio también puede reintentar con 250 m adicionales de margen de grafo
  para fallos compatibles; no mueve las anclas. No confundir el radio nominal
  de 1 km del diseño inicial con una prohibición de ese reintento vigente.

**Desalineación documental pendiente:** la sección 13 de la arquitectura y partes
narrativas de OpenAPI todavía describen el MVP inicial (corte simple y sin fallback
por falta de cobertura). El código actual ya admite multitramos, fallback y
recuperaciones. Esta limpieza conserva esas diferencias explícitas; no cambia
políticas ni pretende resolverlas eliminando comportamiento implementado.

## Operación y validación

La API requiere `DATABASE_URL` y **`TOMTOM_API_KEY`** al iniciar. Los demás parámetros
son constantes revisables en `internal/config/parameters.go`: radios, tolerancias,
zoom, caché y límites de 10.000 posiciones, 20 km de corte y 32 tiles por consulta.
`cmd/api` comparte un único cliente/caché TomTom entre requests.

El grafo y las restricciones se preparan externamente; carga y límites en
[`sql/calles/README.md`](../sql/calles/README.md). No recargar ni preparar datos
durante un request. La red no garantiza por sí sola que un colectivo pueda
circular: revisar restricciones no modeladas, terminales y accesos internos.

```sh
go test ./...
go build ./...
go vet ./...
# Opt-in: usar una base de pruebas PostGIS + pgRouting.
TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/database/postgres -run 'TestDetour' -count=1
# Las pruebas normales no llaman a TomTom; la prueba externa exige además:
# TOMTOM_LIVE_TEST=1 y TOMTOM_API_KEY (consume cuota).
```

Se probaron variantes reales de la línea 132 con cortes en Av. Córdoba; no es
una validación exhaustiva. Pendientes: revisión visual de más recorridos/cortes,
latencia fría/caliente, cuota y consumo sostenidos, y sincronizar la descripción
funcional con las ampliaciones actuales. No se conservan contadores históricos
de requests del spike ni sus PBF temporales como documentación operativa.
