# H0012 — Persistencia experimental

Complementa [el estado incremental](h0012_incremental.md) y
[el plan funcional](plan_h0012_cuellos_botella.md). No implementa el detector,
matching incremental, lectura del raw ni el ciclo periódico del worker.

## Esquema y migración

Definiciones compartidas por instalaciones nuevas y migración:
`sql/bottlenecks/crear_tablas.sql`. `cmd/initdb` las ejecuta después de `ddl.sql`,
sin crear modelos activos ni cargar evidencia histórica.

Para una base existente, aplicar explícitamente (requiere PostGIS >= 3.4):

```powershell
psql -h localhost -p 5433 -U postgres -d vialis -X -v ON_ERROR_STOP=1 -f sql/bottlenecks/migrar_persistencia.sql
```

La migración es transaccional e idempotente sobre este esquema; no borra tablas,
no cambia H0011, no corrige equivalencias ni activa un modelo. No actualiza las
otras partes desactualizadas del backup, como combinaciones o restricciones viales.
No hay migración implícita al arrancar el worker.

| Tabla | Contenido |
|---|---|
| `h0012_modelos` | Configuración inmutable, activación, checkpoint global y recepción de última captura confirmada |
| `h0012_trazados` | Identidad GTFS/sentido/hash, geometría completa, longitud y snapshot de paradas ordenadas |
| `h0012_sectores` | Progresivas inicial/final y geometría geodésica por sector |
| `h0012_estados` | Estado derivado mínimo por modelo/trazado/unidad, sin serie GPS ni coordenadas GPS |
| `h0012_mediciones` | Pasajes completos e incompletos, atribución horaria y soporte de calidad |
| `h0012_diagnosticos` | Conteos por captura, confirmados junto con resultados y checkpoint |

No hay FK hacia IDs regenerables de GTFS ni hacia capturas/raw sujetos a purga.
Los snapshots conservan geometría y paradas aunque el catálogo se reemplace.
No se implementa purga de estas tablas durante la etapa experimental.

## Contrato del repositorio

`bottlenecks.PassageStore` es la interfaz de dominio;
`postgres.BottlenecksStore` la implementación. No recibe payloads HTTP.

### Activación explícita

`Activate(PersistenceModel)` requiere nombre, versiones de medición/matching,
longitud nominal, límites de medición, zona Buenos Aires, cierre de cuatro
capturas y días de retención raw. Esos días deben provenir de la misma constante
H0011 al integrar el procesador: se registran para controlar atraso, **no cambian
la retención**. No hay defaults nuevos de producción; las versiones deben cambiar
si cambia la segmentación, geodesia, matching o política de medición.

En una primera activación se excluyen las capturas existentes mediante
`max(id_captura)`. Una sección crítica corta usa el lock de ingesta y un lock
SHARE de la tabla de capturas para incluir inserciones en vuelo. No se conserva
ese lock durante medición ni commits. Repetir la activación con la misma
configuración recupera la marca original y su checkpoint, incluso tras purga.
Reusar el nombre con otra configuración falla con `ErrModelChanged`.

Crear otro nombre de modelo es una activación independiente, sin backfill; no
es una migración automática de resultados ni una reparación de huecos.

### Geometrías versionadas

`RegisterLayout(nombre, idRecorrido)` materializa en una transacción el shape,
sectores medidos por `geography` y paradas. Bloquea escrituras del catálogo
durante la materialización para obtener un snapshot coherente. Una misma identidad/hash/modelo
recupera el snapshot existente; otra geometría crea otro trazado.

Cada parada conserva orden, ID GTFS, nombre y posición estática, más alternativas
locales dentro del radio del modelo con progresiva, distancia y sector candidato.
No se resuelve una ambigüedad de lazo eligiendo el más cercano. Una lista vacía
indica que no hubo asociación dentro del radio, no que el sector no tenga
paradas. Estas alternativas **no acreditan** cuál rama usa la parada ni resuelven
el tratamiento funcional de terminales/regulación.

### Commit por captura

`LoadStates(nombre)` recupera y valida los estados persistidos junto con el
checkpoint del modelo bajo el mismo lock de fila usado por los escritores.

El productor obtiene `CaptureState` y llama a `MeasureCapture` después de resolver
el matching y ordenar la evidencia. Envía sus resultados como `StreamTransition`:

- Nuevo stream: `PreviousCaptureID=0`; estado inicial construido desde la marca
  de activación persistida y sólo datos posteriores a esa marca.
- Stream existente: checkpoint anterior real y estado recuperado, nunca recreado
  desde cero. Todos los streams almacenados deben participar en cada captura,
  incluidos los que no tengan novedades; omitir uno cancela el commit completo.
- `LastCaptureID` y `LastReceivedAt` deben corresponder a la captura real de H0011.
  Las capturas fallidas no tienen fila en esa tabla y no generan commits. Las
  parciales sí se procesan y el repositorio contrasta los contadores de ausencia
  contra `cobertura_parcial`, sin confiar en una bandera enviada por el productor.

`CommitCapture` serializa por modelo con lock de fila. Verifica checkpoint
esperado, siguiente captura retenida y checkpoints de streams. Guarda mediciones,
estados, diagnósticos y checkpoint en una única transacción. Un replay devuelve
`ErrCaptureAlreadyProcessed` antes de escribir; un checkpoint concurrente
incompatible o salto de captura se rechaza. Un fallo de validación, SQL o commit
no entrega resultados parciales como confirmados.

Los resultados completos e incompletos comparten identidad de entrada soportada
(modelo, trazado, unidad, scope, sector e instante exacto UTC); un intento de
insertar dos resultados con esa identidad aborta la transacción. JSONB conserva
el soporte temporal con precisión de nanosegundos. Las columnas `timestamptz`
son auxiliares de consulta y tienen precisión de microsegundos; fecha/hora local
se calculan desde la entrada original, no redondeada. No se publican IDs de
vehículos ni este estado operativo en una API.

El repositorio valida consistencia estructural/de soporte; **no vuelve a ejecutar
el matcher ni puede demostrar que el productor usó todas las observaciones raw**.
La completitud de las transiciones, buffering, orden temporal y cortes por cambios
de identidad/versión son obligaciones del procesador, todavía no implementado.
No usar ventanas batch independientes para producir estos estados.

## Atraso y retención de la fuente

Antes de avanzar se comprueba si la fecha UTC de activación/última recepción
confirmada o la captura objetivo quedó fuera de la retención declarada. En ese
caso se devuelve `ErrSourceExpired`, sin resetear la activación ni saltear a lo
que aún esté retenido. Es una protección conservadora: no prueba que realmente
se hayan perdido datos, y también puede detenerse tras una pausa larga sin
capturas. La recuperación necesita una decisión explícita; no se hace backfill
ni se declara completa una ventana con fuente desconocida.

La comprobación supone que sólo H0011 administra la retención y que su reloj y
el de PostgreSQL concuerdan en la fecha UTC. No detecta borrados arbitrarios de
raw ni restaura geometrías anteriores a una activación; la lectura incremental
deberá verificar disponibilidad/completitud del contexto antes de medir.

## Validación

Tests PostGIS aislados (sin tocar `vialis`, cada test elimina su esquema):

```powershell
$env:TEST_DATABASE_URL = "postgresql://postgres:postgres@localhost:5433/vialis"
go test ./internal/database/postgres -run '^TestPassageStore' -count=1 -v
```

Cubren esquema repetido, activación sin backfill/reinicio, modelos inmutables,
geometría geodésica, paradas y lazos, versiones tras recarga, recuperación de
estado/soporte, pérdida de fuente, parciales, closures, rollback de errores Go y
SQL, doble escritor concurrente, replay e identidad temporal submicrosegundo.
La suite normal `go test ./...`, build y vet también pasan. No se ejecutó race:
el entorno Windows actual no tiene CGO/GCC habilitados.

La migración no se aplicó al esquema local `vialis` ni se activó un modelo real.
La siguiente etapa es lectura incremental y matching provisional/buffering antes
de conectar el ciclo independiente al worker y medir frecuencia/costo.
