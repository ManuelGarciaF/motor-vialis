# H0012 — Corte operacional del 21/10/2026

Inspección de `vialis` en `vialis-motor-db:5432`, entre 23:19 y 23:20 UTC
(20:19–20:20 Buenos Aires). Consultas con `default_transaction_read_only=on`
y timeout; sin modificar datos, reiniciar el worker ni ejecutar otra captura.
Los conteos son cortes de una base activa, no un snapshot único.

## Evidencia observada

- Configuración `operational`, activada a las 21:51:06 UTC; captura inicial 873.
  Política persistida: 500 m / 180 s / 100 m / 100 km/h, cierre de cuatro capturas,
  retención raw de siete fechas UTC.
- Checkpoint 927, inicialmente igual a la última captura. Durante la inspección
  ingresó la 928: atraso de una captura. Los 5.944 estados tenían checkpoint 927.
- 558 trazados y 31.022 sectores materializados.
- 41.937 mediciones: 30.219 completas y 11.718 incompletas; 12.202 sectores con
  algún completo y 3.356 unidades con alguna medición. No son sectores
  territoriales únicos ni sectores elegibles para publicar.
- Incompletos: `gps_gap` 7.509; `unresolved_match` 2.314;
  `missing_updates` 1.476; `scope_changed` 254; `backwards` 155;
  `implausible_speed` 10. No representan viajes perdidos ni demuestran lentitud.
- 9.876 completos con ambos cruces en el mismo intervalo GPS; 9.966 sin GPS
  estrictamente interior. Son soportes admitidos, no motivos nuevos de exclusión.
- 3.445 secuencias OPEN; 1.529 con evidencia más antigua que una hora.
  El raw OPEN más antiguo era de las 21:07:06 UTC, con edad aproximada de 2 h 13 min;
  máximo de 53 observaciones por OPEN. Esta evidencia anterior a la activación
  puede ingresar en capturas nuevas: no implica backfill de capturas anteriores.
- Tamaño total de las cinco tablas, incluidos sus índices: aproximadamente 83 MB.
  Incluye geometría estática; no extrapolar una tasa diaria desde este corte.
- Entre las capturas posteriores a la activación: 54 intervalos entre recepciones,
  mediana 78,3 s, p95 204,4 s, máximo 218,3 s; nueve intervalos mayores de 180 s.
  El intervalo de recepción no es el hueco GPS individual, pero merece revisar
  su relación con los rechazos `gps_gap`. No se midió causalidad ni impacto de H0012.
- La conexión `vialis-bottlenecks` estaba idle en las observaciones. Esto no mide
  duración del ciclo ni descarta contención en otros momentos.

## Qué queda por validar técnicamente

1. Obtener logs del worker actual (`bottlenecks_captures_committed.duration_ms`,
   errores y tiempos de captura) y observar varios ciclos, cierres y reinicios.
   Los logs locales disponibles no documentan esta ejecución. Las tablas no
   conservan una serie histórica de costos del productor.
2. Medir atraso sostenido, CPU/IO, locks y cadencia de captura; investigar los
   intervalos largos sin atribuirlos automáticamente al productor.
3. Vigilar edad del raw OPEN frente a la retención: una unidad continuamente
   activa no confirma prefijos y puede agotar contexto. La política vigente
   detiene H0012, no fuerza un cierre ni resetea checkpoint.
4. Medir crecimiento separando snapshots estáticos, estados y mediciones.
5. Revalidar integración PostGIS en una base/esquemas de prueba, no ejecutar
   indiscriminadamente la suite contra la base del worker activo.

Validación de código en esta revisión: `go test ./...`, `go build ./...`,
`go vet ./...` satisfactorios con Go/GCC vía Nix; `gofmt -l internal cmd sql`
sin salida. Tests DB opt-in no ejecutados. No se modificaron reglas funcionales.

## Decisiones funcionales aún pendientes

El productor mide pasajes; no clasifica cuellos de botella. Antes del detector
se debe acordar un protocolo experimental de calibración: período de observación,
tipos de día y franjas comparables, referencia espacial y tratamiento de paradas,
terminales/regulación. Después, evaluar sensibilidad de longitud/filtros,
mínimos de unidades/fechas/pasajes y umbrales de lentitud/recurrencia.

La consolidación entre líneas y sectores contiguos, deduplicación de evidencia,
ranking y API vienen después. Mantener parámetros actuales para acumular evidencia
no equivale a aprobarlos para publicación. No cambiar la política activa sin una
decisión administrativa explícita.

Plan y contratos: [plan](plan_h0012_cuellos_botella.md),
[productor](h0012_productor.md), [persistencia](h0012_persistencia.md),
[incremental](h0012_incremental.md).
