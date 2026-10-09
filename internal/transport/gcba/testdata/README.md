# Fixtures de posiciones GCBA

Fixtures JSON anonimizadas a partir del contrato observado en
`/colectivos/vehiclePositionsSimple` el 22 de septiembre de 2026.

- `vehicle-positions-normal.json`: respuesta válida representativa.
- `vehicle-positions-empty.json`: respuesta exitosa sin vehículos.
- `vehicle-positions-invalid-fields.json`: registros sintéticos con campos
  obligatorios inválidos para probar rechazo individual.

Los IDs, nombres, destinos, coordenadas y timestamps no pertenecen a registros
del proveedor. Se conserva deliberadamente el campo `tip_id`: así se llama en
la respuesta real, aunque probablemente sea un error tipográfico de `trip_id`.

## Pruebas

Las pruebas normales usan estas fixtures y `httptest.Server`, sin llamar a GCBA:

```bash
go test ./internal/transport/gcba/...
```

La prueba live es opt-in. Requiere las credenciales en el entorno (Go no carga
`.env` automáticamente) y puede tardar varios minutos si el proveedor falla:

```bash
GCBA_LIVE_TEST=1 go test ./internal/transport/gcba -run TestLiveCapture -v
```

El cliente conserva filas stale con `Fresh=false`, rechaza timestamps más de un
minuto en el futuro y deduplica cada respuesta por `id + timestamp`. La
idempotencia entre capturas y la detección de cobertura parcial pertenecen al
worker, todavía no implementado.
