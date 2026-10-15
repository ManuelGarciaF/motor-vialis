# H0012 — Spike inicial de segmentación GPS

Plan: [plan_h0012_cuellos_botella.md](plan_h0012_cuellos_botella.md).
Herramienta: `scripts/spike-h0012-segments.sql` (sólo lectura).

## Ventana y método

Se ejecutó contra la base del proyecto en un snapshot de sólo lectura por corrida.
Ventana UTC fija `[2026-10-09 23:50:52.000001, 2026-10-10 23:50:52.000001)`.
Los tres experimentos usan la misma ventana; el worker no se detuvo.
Resultados JSON locales en `.local/h0012/segments-*.json` (no versionados).

- 142.268 posiciones de 3.608 unidades; todas coinciden con una versión geométrica
  vigente. Esto no mide cobertura del parque de colectivos del AMBA.
- 123.083 posiciones a no más de 100 m del shape.
- Intervalo GPS mediano: 62 s; percentil 90: 180 s.
- 104.045 enlaces admisibles de 138.660 con predecesor.
- 5.271 retrocesos longitudinales; 11.533 huecos superiores a 180 s. Los motivos
  no son excluyentes ni todos implican errores GPS (pueden cambiar identidades).

Proyección métrica EPSG:32721 y punto más cercano sobre el shape; comparación
estricta de identidad GCBA/GTFS, sentido y hash. Avance no negativo, distancia
máxima 100 m, hueco máximo 180 s, velocidad longitudinal máxima 100 km/h.
Son filtros experimentales, no parámetros aprobados del motor.

Los cruces se interpolan linealmente entre GPS. Un pasaje necesita cruces de
ambos límites en una secuencia continua. Se incorpora el remanente final menor
que media longitud al sector anterior. Se redondean longitudes/progresivas a
micrómetros para evitar inestabilidad numérica en límites y remanentes.

## Comparación inicial

| Longitud nominal | Pasajes | Recorridos/sentidos con pasajes | Mediana de duración | Mediana de velocidad | Ambos límites en un único intervalo GPS |
|---|---:|---:|---:|---:|---:|
| 300 m | 134.829 | 509 | 49,1 s | 21,98 km/h | 48,0% |
| 500 m | 75.747 | 507 | 83,3 s | 21,59 km/h | 27,3% |
| 800 m | 42.739 | 502 | 135,1 s | 21,31 km/h | 11,9% |

No comparar los conteos como tasas de éxito: sectores más pequeños generan más
pasajes por el mismo recorrido. Tampoco son muestras independientes: un único
intervalo GPS puede generar varios pasajes cortos.

## Sensibilidad sobre la misma ventana

| Filtro | Pasajes 300 m | Pasajes 500 m | Pasajes 800 m | Velocidad mediana 500 m |
|---|---:|---:|---:|---:|
| Base: 180 s / 100 m | 134.829 | 75.747 | 42.739 | 21,59 km/h |
| Hueco máximo 120 s | 91.494 | 47.646 | 24.255 | 22,81 km/h |
| Distancia máxima 50 m | 131.119 | 73.447 | 41.267 | 21,57 km/h |

Reducir la distancia de matching cambia poco estas métricas agregadas. Reducir
el hueco temporal cambia bastante los pasajes y eleva la velocidad mediana;
podría introducir sesgo hacia trayectorias mejor muestreadas o más rápidas.
No prueba que las duraciones sean precisas ni que sectores individuales sean
estables. Necesitamos comparación por sector y revisión de ejemplos.

## Conclusión provisional

**500 m sigue siendo una hipótesis razonable para continuar, no una selección
cerrada.** En 300 m casi la mitad de los pasajes dependen de un solo intervalo
GPS: el tiempo local refleja la interpolación más que evidencia interior.
800 m mejora soporte temporal, a costa de resolución territorial.

El spike todavía no identifica cuellos de botella ni mide recurrencia:

- La proyección por punto más cercano no resuelve lazos ni matching ambiguo.
- No separa esperas de terminal ni compara densidad de paradas.
- No valida hora exacta de entrada/salida frente a una fuente independiente.
- Sólo incluye raw retenido y geometrías vigentes; no analiza cobertura por día.
- Las medianas globales mezclan sectores y horarios y no son referencias de
  detección. No se puede inferir de ellas ausencia de congestión.

## Verificación y siguientes pasos

`test-h0012-segments.py` pasó contra PostGIS usando exclusivamente tablas TEMP:
conteos de límites, remanentes, velocidad uniforme, huecos, versiones de shape,
posiciones fuera del corredor, retrocesos y entrada vacía. `git diff --check`
sin errores. No se modificó código del motor ni datos persistentes.

Continuar con distribución por sector/fecha/franja, ejemplos de trayectorias
(incluyendo lazos y terminales), sensibilidad por sector y comparación de
pasajes con/sin observaciones interiores. Después calibrar referencia espacial,
recurrencia, consolidación entre líneas y ranking. La inspección de geometrías
reales sigue pendiente; no habilitar API productiva con este matcher.
