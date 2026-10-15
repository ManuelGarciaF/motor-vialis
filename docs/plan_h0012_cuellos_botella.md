# H0012 — Plan de detección de sectores recurrentemente lentos

## Objetivo y alcance acordado

Identificar lugares del AMBA donde los colectivos circulan recurrentemente muy
lento, potencialmente por condiciones viales, para priorizar estudios de
intervenciones. No detectar incidentes puntuales ni demostrar causas con GPS.
La unidad publicada es el sector territorial, no el evento ni la línea.
La cobertura se limita a unidades observadas y vinculadas al catálogo.

## Estado actual y preparación para implementar

**Listos para comenzar la implementación técnica incremental, no para publicar
un detector definitivo.** El análisis validó que, sin relajar filtros ni corregir
mappings, existe una base importante de pasajes completos aprovechables. La
segmentación, matching conservador, medición y cobertura ya tienen implementación
experimental y validaciones; no hay que comenzar nuevamente desde cero.

La siguiente etapa propuesta es persistir sectores versionados y mediciones
antes de la purga del raw, con procesamiento incremental reproducible. El usuario
aprobó conservar **pasajes individuales y metadatos de calidad, sin archivar GPS
adicionales**, y arrancar con **datos nuevos, sin backfill**. Esto permite
recalibrar la detección sobre los pasajes, no rehacer su medición con otra
segmentación/matching después de purgar el GPS. La arquitectura concreta y el
tratamiento de correcciones posteriores al cierre aún deben precisarse. Esto permite acumular días para calibrar la detección, sin fijar
prematuramente qué sectores serán elegibles o cómo se ordenará el Top 10.

### Hallazgos que condicionan el diseño

Corrida completa sobre `[2026-10-08T17:01:10Z,2026-10-11T00:46:52.000001Z)`:

| Evidencia validada | Resultado | Consecuencia |
|---|---:|---|
| Población analizada | 515.592 GPS / 563 recorridos-sentidos | Cobertura de targets observados y equivalenciados, no toda la oferta AMBA |
| Pasajes completos válidos | **271.606** | La medición resulta aprovechable con los filtros actuales |
| Sectores con algún pasaje | **25.143 / 31.306 (80,31%)** | Hay base longitudinal amplia, pero tener un pasaje no acredita suficiencia |
| Sectores con evidencia pero sin pasajes completos | 2.304 | No deben mostrarse como circulación normal |
| Sectores sin evidencia reconstruida | 3.859 | Deben distinguirse como sin datos |
| Mediana por celda sector/fecha/hora con pasajes | **2 pasajes / 2 unidades** | La evidencia horaria suele ser escasa; no publicar todo por defecto |
| Pares sector/hora con pasajes en ambas fechas | 41.124 | Repetición de evidencia, no detección de lentitud recurrente |
| Pares con al menos 5 unidades en cada fecha | 3.050 | Corte descriptivo de volumen, **no un mínimo aprobado** |
| Pasajes con ambos cruces en un intervalo GPS | 82.140 (30,24%) | Excluir los que no tienen GPS interior perdería evidencia aprobada |
| Entradas soportadas incompletas | 52.331, de ellas 6.176 censuradas por fin de lote | No equivalen a viajes reales perdidos; preservar motivo y censura |

Los sectores pertenecen a recorridos/versiones, **no son lugares territoriales
únicos**: distintas líneas pueden medir un mismo corredor. La consolidación
territorial sigue pendiente; estos conteos no son conteos de problemas viales.

En aquella corrida de cobertura sólo hay **jueves 08/10 y sábado 10/10, ambos
parcialmente observados**, sin GPS del 09/10 local. Es una ventana histórica fija;
la auditoría posterior de retrasos del 14/10 ya dispone de más datos retenidos. No alcanza para validar recurrencia habitual ni para asumir
comparabilidad entre días hábiles y fin de semana. La elegibilidad debe distinguir
falta de evidencia de ausencia de lentitud; no se infiere oferta de servicio de
las celdas vacías.

Los 69.118 GPS fuera del shape asignado (13,41%) de la auditoría espacial no
justifican corregir equivalencias ni ampliar el radio. Estar cerca de otro shape
no identifica servicio ni sentido. Tampoco se divide el número de pasajes por el
número de GPS para producir una supuesta tasa de cobertura de viajes.

### Decisiones confirmadas y límites aún experimentales

- Identidad mediante equivalencias activas H0011, ruta **y sentido**; IDs GCBA y
  GTFS son namespaces distintos. Nunca mezclar continuidad de identidades.
- Geodesia y matching conservador con alternativas locales y evidencia temporal;
  no desempatar eligiendo el candidato más cercano.
- Velocidad comercial entre cruces interpolados, incluyendo detenciones;
  aceptar pasajes con y sin GPS interior y conservar su soporte.
- Una hora local `America/Argentina/Buenos_Aires`; atribución única por entrada
  estimada, incluso al cruzar una hora o fecha.
- Experimentos con 500 m y límites 180 s / 100 m / 100 km/h. **No quedan aprobados
  por esta corrida como parámetros definitivos de producción.**
- Presupuesto técnico global 15 min, 5 min por target; no modifica el máximo
  hueco GPS de 180 s. La corrida completa terminó en 8 min 23,6 s.
- No modificar H0011, mappings ni filtros; no extrapolar entradas desconocidas.
- Persistir sólo pasajes/metadatos, sin nuevo archivo GPS; iniciar con capturas
  nuevas, sin backfill del histórico analizado.
- Cerrar pendientes como incompletos tras **4 capturas correctas y no parciales
  consecutivas sin timestamp GPS nuevo de la unidad**. Errores no cuentan;
  misma coordenada con timestamp nuevo sí reinicia el contador. No modifica
  el máximo hueco GPS de 180 s ni implica atribuir lentitud.

Resultados y controles completos:
[h0012_cobertura_pasajes.md](h0012_cobertura_pasajes.md).

## Detección propuesta

1. Reconstruir pasajes por sector a partir de secuencias GPS.
2. Medir velocidad comercial como longitud / tiempo de atravesamiento,
   incluyendo detenciones interiores. No promediar velocidad instantánea.
3. Comparar tiempo por km con sectores comparables del mismo recorrido y sentido;
   considerar paradas y separar terminales/regulación. Una diferencia espacial
   no demuestra pérdida respecto de la operación histórica normal.
4. Exigir una proporción relevante de pasajes lentos en distintas unidades,
   fechas y franjas horarias comparables. Múltiples muestras de una unidad
   detenida no constituyen recurrencia independiente.
5. Consolidar evidencia coincidente entre líneas sobre el mismo corredor y
   sentido. Varias líneas refuerzan la hipótesis vial, pero no son obligatorias.

La comparación histórica puede incorporarse después para medir cambios e
intervenciones. Los umbrales de lentitud, recurrencia y evidencia aún deben
calibrarse. No hay todavía un score ni un ranking definitivo.

### Medición aprobada después del spike

Se acepta calcular velocidad comercial mediante distancia longitudinal y tiempo
entre cruces interpolados en ambos casos: con y sin GPS interior. No descartar
por defecto los pasajes sin observaciones interiores. Conservar el soporte de
cada cruce y el conteo interior para analizar sensibilidad y calidad, sin
presentar la interpolación como velocidad instantánea observada.

## Segmentación acordada como hipótesis inicial

- Cortar la geometría real por distancia longitudinal, por recorrido, sentido y
  versión geométrica; no por distancia recta ni directamente entre paradas.
- Probar sectores nominales de **300, 500 y 800 m**. **500 m** es el punto de
  partida, no un parámetro aprobado para producción.
- Unir el remanente final muy corto al sector anterior. El spike usa como
  definición explícita un remanente menor a la mitad de la longitud nominal.
- Guardar geometría, longitud real, progresivas inicial/final, identidad de
  recorrido/versión y paradas incluidas.
- Estimar cruces de límites sólo entre observaciones suficientemente cercanas
  en tiempo y distancia. Rechazar huecos, retrocesos, saltos y matching dudoso.
- No convertir desapariciones del feed en pasajes lentos.
- Conservar controles específicos para lazos, cruces y pasajes repetidos.
- Sectores contiguos con patrones compatibles pueden consolidarse; los límites
  de medición no delimitan necesariamente el problema físico.
- Entre líneas, consolidar por corredor y sentido, no por mera cercanía.

## Salida futura

Mapa de sectores elegibles y Top 10 consistente con el mismo período y política.
Cada sector: geometría, sentido, líneas, velocidad típica, referencia espacial,
diferencia relativa, porcentaje de pasajes afectados, días/franjas observados y
calidad/cobertura. Distinguir sin datos de ausencia de degradación.
El ranking debe evitar favorecer simplemente a sectores con más muestras;
criterio final pendiente. No inferir pasajeros afectados ni causas demostradas.

## Arquitectura

Reutilizar la ingesta H0011; procesamiento fuera de requests HTTP; persistir
pasajes y agregados antes de la purga del raw. El raw actual conserva siete
fechas UTC y no archiva geometrías históricas completas. Los cambios de catálogo
requieren identidades y geometrías versionadas. No modificar H0011 ni sus filtros
para implementar H0012.

## Spike de viabilidad

Solo lectura, sobre raw retenido y versiones que coincidan con el catálogo actual.
Comparar 300/500/800 m con una misma ventana [from,to), sin elegir sólo líneas
con buenos resultados. Medir:

- Cobertura de raw con geometría vigente, unidades y recorridos.
- Intervalos GPS, distancia al shape, retrocesos y saltos implausibles.
- Pasajes reconstruidos por longitud; duración y cantidad de muestras.
- Cuántos pasajes se apoyan en un único intervalo GPS que cruza ambos límites.
- Sensibilidad a máximos de hueco y desplazamiento; no confundir estabilidad
  agregada con exactitud del tiempo interpolado.

La proyección inicial por punto más cercano es exploratoria: no resuelve lazos
ni matching ambiguo. Sus resultados no autorizan publicar detecciones.
Los parámetros experimentales deben quedar registrados en el resultado.

## Criterios para avanzar

Elegir la menor longitud con suficiente evidencia y estabilidad. Inspeccionar
trayectorias y geometrías de ejemplos reales, especialmente lazos y terminales.
Validar después referencias comparables, recurrencia y consolidación territorial.
No implementar API definitiva antes de comprobar la calidad de medición.

## Primera implementación — rama `feature/h0012-cuellos-botella`

Base de dominio implementada en `internal/bottlenecks`:

- `NewLayout`: intervalos longitudinales versionados, longitud nominal explícita,
  remanente corto unido al anterior y límites estabilizados a micrómetros.
- `Measure`: reconstrucción batch determinista de pasajes completos desde
  progresivas ya resueltas por un matcher; interpolación de entrada/salida y
  velocidad comercial, incluyendo detenciones y ambos tipos de evidencia.
- Metadatos: timestamps que soportan cada cruce, observaciones estrictamente
  interiores, máximo hueco de soporte y ambos cruces dentro de un mismo intervalo.
- Cortes de continuidad: matching no resuelto/ambiguo, otra versión, distancia
  excesiva al shape, progresiva fuera del trazado, cambio de identidad proveedor,
  hueco GPS, retroceso o velocidad longitudinal implausible.
- Orden de entrada irrelevante; sin mutar al caller. Duplicados idénticos por
  vehículo/timestamp se ignoran; conflictos se rechazan, no se elige una muestra
  arbitrariamente. Identidades GCBA distintas no comparten continuidad.
- Cruce coincidente con un GPS usa su timestamp sin interpolación. Una espera
  exactamente en el límite se asigna al sector siguiente; no se cuenta como GPS
  estrictamente interior. Terminales/regulación aún necesitan tratamiento propio.
- Sin extrapolación de pasajes incompletos. Tras un corte se inicia una nueva
  secuencia; no se une la evidencia a ambos lados de un hueco.

En esta primera entrega la política de medición se recibió explícitamente, sin
defaults de producción ni cambios al worker, estimadores de simulación o configuración.
Los límites defensivos de recursos/precisión de `NewLayout` no son criterios
funcionales de lentitud. Las estructuras de dominio no congelan contrato HTTP.

### Validación de esta entrega

En esta primera entrega, tests unitarios del paquete: 100% de cobertura de
sentencias; race detector sin errores. Este porcentaje corresponde al dominio
inicial, no es una medición actual del paquete ampliado. `go test ./...`, `go build ./...` y `go vet ./...` pasaron usando Go y GCC
mediante Nix. Los tests DB opt-in no se ejecutaron en esta entrega. Fuentes de
`internal`, `cmd` y `sql` formateadas. `gofmt -l .` incluye un archivo preexistente
no versionado en `.local/bunching-radius-estimate/main.go`; no se modificó.

## Segunda entrega — análisis de sólo lectura

Decisiones consultadas y confirmadas: matching conservador secuencial,
herramienta sólo lectura, distancias geodésicas, límites experimentales del spike
(180 s / 100 m / 100 km/h) y todas las alternativas locales dentro del radio.

Implementados matcher temporal, interfaz de repositorio, lectura PostGIS en
snapshot read-only/repeatable-read y `cmd/bottlenecks-analysis`, sin cambios a
worker, esquema o API. Ejecutado sobre tres recorridos reales; pasó validación
unitaria, integración aislada PostGIS, build, vet y race. Métodos, comandos y
resultados en [h0012_analisis.md](h0012_analisis.md).

### Auditoría solicitada después de la segunda entrega

Se auditó versión, distancia geodésica y contabilidad de candidatos para los tres
recorridos. Todos los unmatched de estas muestras se explican por posiciones
fuera del radio; en 622/R3 son 591, con 307 a más de 2 km. Se encontraron cuatro
identidades GCBA asignadas al mismo GTFS, tres con mapping histórico ambiguous.
No se modificaron equivalencias ni filtros. Con autorización del usuario se
compararon las cuatro identidades contra todo el catálogo: no aparece reemplazo
inequívoco y 331 de las 591 posiciones exteriores no están a 100 m de ningún
shape GTFS. Las correcciones de mapping no están justificadas con esta evidencia.
Hallazgos y límites en
[h0012_auditoria_matching.md](h0012_auditoria_matching.md).

### Auditoría global con equivalencias H0011

Se extendió el análisis mediante la tabla activa `equivalencias_recorridos_gcba`,
cruzando ruta/sentido GCBA con destino/sentido GTFS explícitos y verificando los
metadatos históricos. No se asume igualdad entre IDs de API y GTFS.

En 515.592 posiciones retenidas, 629 identidades GCBA/sentido y 563 destinos GTFS,
69.118 (13,41%) están fuera del shape asignado: 53.691 cerca de otro shape y
15.427 sin ningún shape a 100 m. Hay exteriores en 547 destinos; no es un caso
único. Todas las posiciones coinciden con el mapping y hash vigentes en esa
ventana, lo que no demuestra que cada equivalencia sea operativamente correcta.

Con aprobación del usuario se construyó un índice TEMP por fragmentos de vértices
originales, sin cambiar tablas persistentes ni geodesia. Pasaron tests aislados y
controles sobre datos reales. Método, ventanas, exposición temporal y resultados:
[h0012_auditoria_global.md](h0012_auditoria_global.md).

### Cobertura de pasajes completos por sector, fecha y hora

Usuario confirmó franjas de una hora local Buenos Aires y atribución única por
entrada estimada. Se agregó diagnóstico de entradas soportadas incompletas y
censura de lote, sin inferir viajes perdidos ni cambiar la medición aprobada.
El comando `-all` selecciona targets mediante las equivalencias H0011 y emite
NDJSON con manifiesto/versiones/conteos y marcador de finalización.

Tras autorización del usuario, el presupuesto global pasó de 5 a 15 minutos,
conservando 5 minutos por target y los filtros 180 s / 100 m / 100 km/h. Terminó
en 8 min 23,6 s: 563 targets, 515.592 posiciones, 271.606 pasajes completos;
25.143 de 31.306 sectores con alguna medición válida (80,31%). La mediana por
celda horaria con pasajes es 2 pasajes / 2 unidades. Los datos son de jueves 8/10
y sábado 10/10, ambos parciales; no se decidió una política de publicación o
recurrencia a partir de esto.

Ver metodología, volumen, interrupciones, validación y límites en
[h0012_cobertura_pasajes.md](h0012_cobertura_pasajes.md).

## Próximas etapas propuestas y condiciones de avance

### 1. Persistencia y procesamiento incremental — siguiente etapa técnica

**Aprobado:** conservar pasajes individuales y sus metadatos de calidad, **no
archivar series GPS adicionales**. No modificar el raw/retención H0011. Se acepta
que, tras su purga, podrán recalibrarse clasificación/recurrencia sobre pasajes,
pero no volver a medirlos con otra segmentación o matching. La geometría estática
de sectores/versiones no es un historial de ubicaciones GPS.

**Inicio:** usar datos nuevos desde la activación, sin backfill. Especificar la
marca inicial y checkpoints para que los reinicios no vuelvan a incluir datos
anteriores a esa activación ni dupliquen pasajes.

**Cierre aprobado:** 4 capturas correctas y no parciales consecutivas sin un
timestamp GPS nuevo de la unidad. Procesar las actualizaciones antes de sumar
ausencias; los errores no cuentan y una coordenada idéntica con timestamp nuevo
sí es actualización. No contar ejecuciones H0012 en lugar de capturas. El cierre
es incompleto, no una salida inferida ni una medición de lentitud. Sigue vigente
el máximo hueco de soporte de 180 s y los demás cortes de continuidad.

Auditoría read-only del 14/10: antigüedad GPS mediana 67 s, p95 146 s, p99 196 s;
consultas de unos 3,5 s. En 989.762 pares temporalmente compatibles, 17 llegaron
tras 3 capturas sin actualizaciones y ninguno tras 4; esto motivó consultar y
aprobar 4. No prueba que nunca habrá datos posteriores al cierre ni que esos
pares fueran pasajes espacialmente válidos. Método y límites:
[h0012_retraso_gps.md](h0012_retraso_gps.md).

**Ejecución en discusión:** el usuario propone aprovechar el mismo proceso
`realtime-worker`, con una goroutine periódica H0012. Es una alternativa viable
para simplificar despliegue, no una autorización todavía para modificar el worker.
La separación necesaria es lógica: no ejecutar el cálculo pesado dentro del ciclo
de captura ni de su transacción H0011. Actualmente ese ciclo es serial y espera
al finalizar captura/almacenamiento, por lo que agregarle H0012 sin separación
retrasaría las siguientes capturas.

Si se adopta el mismo proceso: un único ciclo H0012 sin solapamientos, checkpoints
propios, cancelación/apagado coordinados y reintentos independientes de fallos
operativos; limitar concurrencia y consumo de conexiones. Sigue compartiendo CPU,
memoria y base de datos con la ingesta: una goroutine no aísla recursos ni fallos
de proceso. Medir carga sobre la captura antes de dar por segura la integración.
No fijar `N` minutos hasta medir costo y atraso del procesamiento incremental.

El backfill de 515.592 GPS tardó 8 min 23,6 s; no debe repetirse completo cada
`N` minutos. Procesar sólo novedades y el contexto pendiente debería reducir
trabajo, pero su costo aún no se midió. Crear una goroutine no es el costo
principal: importa el trabajo PostGIS y la persistencia.

Antes de implementar, precisar esquema/retención de pasajes y metadatos,
checkpoints/estado operativo mínimo, tratamiento de GPS tardíos posteriores al
cierre e integración del procesador. El contador de cierre ya fue acordado;
no confundirlo con una autorización para archivar GPS ni con un plazo universal
de llegada.
Requisitos ya identificados:

- Materializar geometría de sectores, longitudes, progresivas y relación con
  paradas; conservar versiones de geometría y modelo/política de medición.
- Persistir mediciones y soporte de calidad antes de la purga de siete fechas
  UTC. Los hashes actuales no permiten recuperar una geometría histórica borrada.
- Reprocesar sin duplicar pasajes ni inflar unidades distintas; no mezclar
  versiones, políticas o scopes entre lotes.
- Conservar la continuidad y la evidencia temporal necesaria entre ejecuciones:
  el fin de un lote técnico no debe perder un pasaje ni generar censura repetida.
  No basta con ejecutar el batch actual sobre ventanas independientes.
- Manejar observaciones tardías y matching pendiente sin congelar prematuramente
  una ambigüedad; documentar qué resultados son provisionales o finales.
- Mantener el procesamiento fuera de HTTP y no alterar el worker/filtros H0011
  sin autorización. Consultar cómo integrar el nuevo procesador.

Validar idempotencia, recuperación tras interrupciones, cambios de versión y
paridad batch/incremental sobre entradas equivalentes con la misma política de
recepción/cierre, además de integración PostGIS aislada. Un resultado parcial nunca se publica como agregado completo.

### 2. Calibración y elegibilidad — decisiones funcionales pendientes

Acumular más fechas y revisar trayectorias reales, especialmente lazos,
terminales/regulación y sectores con poca evidencia. La cantidad/duración mínima
del período aún debe acordarse; dos fechas parciales de tipos distintos no la
resuelven. No hace falta esperar para construir la persistencia técnica, pero sí
para validar una política de recurrencia.

Consultar antes de elegir:

- Longitud nominal de producción y filtros definitivos; 300/500/800 m siguen
  siendo alternativas experimentales, sin comparación global horaria completada.
- Días/franjas comparables y tratamiento de hábiles, sábados, domingos/feriados.
- Mínimos de pasajes, unidades distintas, fechas y cobertura para elegibilidad.
- Referencia espacial de lentitud y tratamiento de paradas/terminales/regulación.
- Umbrales de lentitud, proporción de pasajes afectados y regla de recurrencia.
- Tratamiento y comunicación de soporte interpolado, censura y sesgo de selección.

Validar sensibilidad y ejemplos reales antes de congelar parámetros. Los cortes
1/2/3/5/10 del informe son descripciones del volumen, no decisiones de publicación.

### 3. Consolidación territorial y ranking — después de validar detección

Acordar correspondencia entre sectores de distintas líneas, corredor/sentido,
fusión de sectores contiguos y deduplicación de evidencia compartida. Definir
score y desempates reproducibles sin premiar sólo mayor cantidad de GPS.
Validar que el mapa y el Top 10 usan la misma elegibilidad, período y política.
No inferir pasajeros afectados ni atribuir causa vial demostrada.

### 4. API y publicación — después de validar elegibilidad y ranking

Definir contrato para geometría, calidad, cobertura y estados sin datos/no
elegible/elegible. Publicar agregados sin IDs de unidades ni GPS individuales.
No fijar ahora una API definitiva ni tratar falta de detecciones como ausencia
de problemas.

**Este plan actualizado no autoriza políticas nuevas ni cambios de datos.** Las
etapas requieren las consultas indicadas; la historia H0012 aún no está completa.
