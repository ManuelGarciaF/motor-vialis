# Vialis Motor: funcionamiento de la simulación

**Alcance:** visión funcional y conceptual del motor completo  
**Estado documentado:** funcionamiento implementado actualmente  
**Última actualización:** 15 de agosto de 2026

## Contenido

1. [Objetivo del motor](#1-objetivo-del-motor)
2. [Qué información utiliza](#2-qué-información-utiliza)
3. [Flujo general de una simulación](#3-flujo-general-de-una-simulación)
4. [Definición de la ruta a simular](#4-definición-de-la-ruta-a-simular)
5. [Validación de la ruta](#5-validación-de-la-ruta)
6. [Estimación de demanda](#6-estimación-de-demanda)
7. [Cálculo de distancia](#7-cálculo-de-distancia)
8. [Estimación del tiempo de viaje](#8-estimación-del-tiempo-de-viaje)
9. [Fuentes y confianza](#9-fuentes-y-confianza)
10. [Cálculo de recaudación potencial](#10-cálculo-de-recaudación-potencial)
11. [Resultado de la simulación](#11-resultado-de-la-simulación)
12. [Comparación de dos rutas](#12-comparación-de-dos-rutas)
13. [Ejemplo conceptual](#13-ejemplo-conceptual)
14. [Preparación y actualización de datos](#14-preparación-y-actualización-de-datos)
15. [Decisiones funcionales](#15-decisiones-funcionales)
16. [Alcance actual y limitaciones](#16-alcance-actual-y-limitaciones)
17. [Evolución prevista](#17-evolución-prevista)
18. [Glosario](#18-glosario)
19. [Conclusión](#19-conclusión)

## 1. Objetivo del motor

Vialis Motor permite evaluar una línea de transporte propuesta antes de su
implementación.

La entrada es una ruta ordenada, formada por paradas, el recorrido exacto
entre ellas y la jurisdicción tarifaria aplicable. A partir de esa
información, el motor estima:

- Cuántos viajes existentes podrían ser atendidos por la nueva línea.
- Qué proporción de esa demanda resulta realmente accesible desde sus paradas.
- Cuántos kilómetros recorre la línea.
- Cuánto podría demorar el viaje.
- Qué tan confiable es la estimación del tiempo en cada zona.
- Qué recaudación potencial podría generar esa demanda, según el cuadro
  tarifario de la jurisdicción y los supuestos de captación configurados.

El motor no genera automáticamente el recorrido ni decide dónde deben ubicarse
las paradas. Evalúa una propuesta ya definida.

Tampoco intenta predecir con exactitud la operación futura. Sus resultados son
estimaciones comparativas construidas con:

- Movilidad observada y expandida estadísticamente.
- Oferta programada de transporte público existente.
- Geometría de la ruta propuesta.
- Cuadro tarifario vigente de la jurisdicción declarada.

La finalidad principal es apoyar análisis de viabilidad y comparación de
escenarios.

### 1.1. Preguntas que responde

Para una ruta propuesta, el motor puede responder:

1. ¿Qué distancia total recorre?
2. ¿Qué zonas y movimientos de pasajeros quedan próximos a sus paradas?
3. ¿Cuál es la demanda bruta existente entre esas zonas?
4. ¿Cuál es la demanda potencial después de considerar accesibilidad?
5. ¿Cuánto demoraría el recorrido en un escenario rápido, típico o lento?
6. ¿Qué tramos tienen referencias locales sólidas?
7. ¿En qué tramos fue necesario recurrir a un promedio general?
8. ¿Qué recaudación potencial generaría, según la jurisdicción y los
   supuestos de captación y mezcla de pago configurados?
9. ¿Cuánto aporta cada parada a la demanda y a la recaudación de la ruta?
10. Frente a una ruta ya definida, ¿qué efecto tiene modificarla? (sección 12)

### 1.2. Qué todavía no responde

En el estado actual no calcula:

- Captación real (cuántos de los pasajeros accesibles elegirían efectivamente
  la línea, más allá del supuesto fijo de captación).
- Costos operativos.
- Cantidad necesaria de vehículos.
- Frecuencia óptima.
- Comparación automática con líneas completas similares.
- Congestión en tiempo real.

Estas capacidades pueden incorporarse sobre las métricas existentes.

## 2. Qué información utiliza

El motor combina cuatro grupos de información.

```mermaid
flowchart LR
    MOV["Movilidad existente<br/>viajes y expansión"] --> MOTOR["Vialis Motor"]
    GTFS["Transporte existente<br/>recorridos y horarios GTFS"] --> MOTOR
    TARIFAS["Cuadro tarifario<br/>por jurisdicción"] --> MOTOR
    ROUTE["Propuesta nueva<br/>paradas, jurisdicción<br/>y recorrido exacto"] --> MOTOR
    MOTOR --> RESULT["Demanda, distancia,<br/>tiempo, recaudación<br/>y confianza"]
```

### 2.1. Viajes de transporte público

La base de movilidad representa viajes de un día hábil típico. Cada registro
contiene, entre otros datos:

- Origen.
- Destino.
- Modos utilizados.
- Cantidad de etapas.
- Franja horaria.
- Factor de expansión.

Una fila de la fuente no equivale necesariamente a un único viaje de toda la
población. El factor de expansión indica cuántos viajes representa
estadísticamente.

Por eso, para estimar demanda se suman factores de expansión y no solamente
filas.

### 2.2. Agregación territorial H3

Los orígenes y destinos se agrupan mediante celdas hexagonales H3 de resolución 8.

H3 permite:

- Trabajar con zonas regulares.
- Agregar viajes sin consultar cada registro individual.
- Vincular paradas con áreas cercanas.
- Construir una matriz origen-destino.

Cada celda conserva un punto de máxima concurrencia. Ese punto representa la
ubicación de mayor peso estadístico dentro del hexágono y se utiliza para medir
la accesibilidad desde las paradas.

No representa:

- El centro geométrico de la celda.
- La demanda total de toda la celda.
- Personas presentes simultáneamente.

### 2.3. Matriz origen-destino

La matriz agrega la cantidad estimada de viajes entre cada par de celdas:

```text
celda de origen → celda de destino → viajes estimados
```

La dirección importa. Los viajes de `A → B` y `B → A` son movimientos
distintos.

Esta matriz es la base del cálculo de demanda de una ruta.

### 2.4. Datos GTFS

GTFS aporta la descripción programada del transporte existente:

- Líneas y ramales.
- Sentidos de circulación.
- Viajes programados.
- Secuencias de paradas.
- Horarios de llegada y salida.
- Geometrías de recorridos.

El motor transforma esa información en referencias de tiempo comercial por
tramo.

Los horarios GTFS son programados. No son mediciones GPS ni registros de
tráfico real.

### 2.5. Ruta propuesta

La propuesta contiene:

- Jurisdicción tarifaria.
- Paradas ordenadas.
- Identificador de cada parada.
- Posición de cada parada.
- Recorrido exacto desde cada parada hasta la siguiente.

La ruta representa un solo sentido de circulación. Una eventual vuelta debe
simularse como otra ruta ordenada.

### 2.6. Cuadro tarifario

El cuadro tarifario define, para cada jurisdicción, bandas de distancia con
una tarifa para tarjeta registrada y una tarifa sin registrar
(`vialis.tarifas_colectivo`). Es la referencia que el motor usa para traducir
demanda potencial en recaudación potencial (sección 10).

Igual que GTFS y la movilidad, es información preparada de antemano: la
simulación no define tarifas, las consulta.

## 3. Flujo general de una simulación

```mermaid
flowchart TD
    INPUT["1. Recibir ruta propuesta"] --> VALIDATE["2. Validar paradas,<br/>geometrías y jurisdicción"]
    VALIDATE --> DEMAND["3. Estimar demanda"]
    VALIDATE --> DISTANCE["4. Medir distancia"]
    DISTANCE --> TIME["5. Estimar tiempo<br/>por tramo"]
    DEMAND --> REVENUE["6. Estimar recaudación<br/>potencial"]
    TIME --> REVENUE
    DEMAND --> MERGE["7. Integrar resultados"]
    TIME --> MERGE
    REVENUE --> MERGE
    MERGE --> OUTPUT["8. Entregar totales<br/>y detalle"]
```

### 3.1. Recepción

El motor recibe la ruta completa. No alcanza con una lista de coordenadas de
paradas: también debe conocer el camino real entre cada par consecutivo y la
jurisdicción tarifaria aplicable.

### 3.2. Validación

Se comprueba que la ruta sea coherente y pueda evaluarse sin corregirla
silenciosamente.

### 3.3. Demanda

Las paradas se vinculan con zonas H3 próximas. Después se consulta cuántos
viajes se producen entre esas zonas en el mismo sentido que la ruta.

### 3.4. Distancia

Cada recorrido entre paradas se mide geográficamente y las distancias se suman.

### 3.5. Tiempo

Cada tramo busca recorridos de transporte existentes que:

- Pasen por una zona próxima.
- Tengan una dirección similar.
- Posean tiempos programados válidos.

Esas referencias se usan para estimar tres escenarios.

### 3.6. Recaudación

Para cada par de paradas con demanda potencial se busca la banda tarifaria de
la jurisdicción declarada según la distancia acumulada de ese par, y se
aplican los supuestos de captación y mezcla de pago configurados.

### 3.7. Integración

El detalle por par de paradas y por tramo se colapsa en dos ejes: los totales
de la ruta y el aporte de cada parada (sección 11). Si la ruta es inválida o se
produce un error de datos, no se entrega un resultado parcial.

Cuando se comparan dos rutas, este flujo se ejecuta completo para cada una y
después se calcula la diferencia (sección 12).

## 4. Definición de la ruta a simular

### 4.1. Jurisdicción

Cada ruta declara una jurisdicción tarifaria junto con las paradas: `caba`,
`province` o `national`. Determina qué cuadro tarifario se usa para calcular
la recaudación potencial (sección 10). No se infiere de las coordenadas.

```json
{
  "jurisdiction": "caba",
  "stops": []
}
```

### 4.2. Paradas ordenadas

El orden de las paradas tiene significado operativo.

Para una ruta:

```text
A → B → C → D
```

el motor considera viajes:

```text
A → B
A → C
A → D
B → C
B → D
C → D
```

No considera automáticamente viajes en sentido contrario.

### 4.3. Recorrido hacia la siguiente parada

Cada parada, salvo la última, contiene un recorrido `PathToNext`.

```text
Parada A
  └── recorrido A → B
Parada B
  └── recorrido B → C
Parada C
  └── sin recorrido siguiente
```

El formato geográfico es un `LineString` GeoJSON.

Esta representación permite:

- Medir cada tramo por separado.
- Adaptar la velocidad estimada a cada zona.
- Identificar origen y destino del tramo.
- Sumar resultados sin volver a dividir la geometría.

### 4.4. Coordenadas

Las posiciones de parada se expresan con campos nombrados:

```json
{
  "latitude": -34.6037,
  "longitude": -58.3816
}
```

GeoJSON utiliza el orden:

```text
[longitud, latitud]
```

Ejemplo:

```json
{
  "type": "LineString",
  "coordinates": [
    [-58.3816, -34.6037],
    [-58.3764, -34.6051],
    [-58.3712, -34.6083]
  ]
}
```

### 4.5. Paradas repetidas

Una misma ubicación física puede aparecer más de una vez en un recorrido, por
ejemplo en una línea circular.

Como los identificadores deben ser únicos dentro de la simulación, cada
aparición debe distinguirse:

```text
parada-123:inicio
parada-123:regreso
```

La identidad dentro de la ruta representa una ocurrencia ordenada, no
necesariamente una parada física diferente.

## 5. Validación de la ruta

La validación protege los resultados antes de consultar demanda, tiempos o
tarifas.

### 5.1. Reglas

| Elemento | Regla |
|---|---|
| Jurisdicción | Debe ser `caba`, `province` o `national`. |
| Ruta | Debe contener al menos dos paradas. |
| Identificadores | Deben ser no vacíos y únicos. |
| Latitud | Debe ser finita y estar entre -90 y 90. |
| Longitud | Debe ser finita y estar entre -180 y 180. |
| Recorrido intermedio | Es obligatorio. |
| Última parada | No debe contener recorrido siguiente. |
| LineString | Debe tener al menos dos puntos. |
| Punto inicial | Debe quedar a no más de 20 m de la parada actual. |
| Punto final | Debe quedar a no más de 20 m de la parada siguiente. |
| Sentido | Debe ir desde la parada actual hacia la siguiente. |
| Longitud | Debe ser mayor que cero. |

### 5.2. Tolerancia de 20 metros

La geometría no necesita empezar y terminar en exactamente el mismo valor
decimal que la parada. Se acepta una diferencia de hasta 20 metros.

Esto contempla:

- Diferencias de precisión entre fuentes.
- Ubicación de la parada sobre la vereda.
- Geometría del recorrido sobre el eje de la calle.

Una diferencia mayor se considera ambigua y se rechaza.

### 5.3. Geometrías invertidas

Un tramo que va de la próxima parada hacia la actual es inválido.

El motor no lo invierte automáticamente porque hacerlo podría ocultar un error
en la construcción de la ruta.

### 5.4. Resultado de una validación fallida

El error indica qué parada, coordenada o campo incumple la regla. Ningún
cálculo de demanda, tiempo o recaudación se inicia con una entrada inválida.

## 6. Estimación de demanda

La demanda intenta medir cuántos desplazamientos existentes podrían ser
atendidos por la ruta propuesta.

No cuenta personas esperando actualmente en una parada. Parte de movimientos
origen-destino agregados para un día típico.

### 6.1. Flujo

```mermaid
flowchart LR
    STOPS["Paradas"] --> CELLS["Buscar zonas H3<br/>cercanas"]
    CELLS --> ASSIGN["Asignar cada zona<br/>a una parada"]
    ASSIGN --> ACCESS["Calcular<br/>accesibilidad"]
    ACCESS --> OD["Consultar matriz<br/>origen-destino"]
    OD --> PAIRS["Demanda por<br/>par de paradas"]
    PAIRS --> TOTAL["Totales de ruta"]
```

### 6.2. Área de influencia

Cada parada tiene actualmente un radio de acceso de 800 metros.

El motor busca zonas H3 próximas y mide la distancia entre la parada y el punto
de máxima concurrencia de la zona.

Solo se aceptan puntos a menos de 800 metros. El límite exacto no se incluye.

El radio representa una distancia máxima de acceso teórica. No modela:

- Barreras físicas.
- Calidad de veredas.
- Cruces inseguros.
- Pendientes.
- Tiempo real de caminata.

### 6.3. Asignación exclusiva

Una zona puede quedar cerca de varias paradas. Para evitar doble conteo, se
asigna a una sola.

La prioridad es:

1. Parada más cercana.
2. Si existe empate, parada que aparece antes en la ruta.
3. Si el empate continúa, identificador menor.

Esto produce resultados deterministas.

También implica que agregar o mover una parada puede redistribuir zonas entre
paradas, incluso si el recorrido general no cambia.

### 6.4. Accesibilidad

La accesibilidad reduce el aporte de una zona cuando su punto principal está
lejos de la parada.

#### Método lineal

```text
accesibilidad = 1 - distancia / radio
```

Ejemplos con radio de 800 m:

| Distancia | Accesibilidad lineal |
|---:|---:|
| 0 m | 1,00 |
| 200 m | 0,75 |
| 400 m | 0,50 |
| 600 m | 0,25 |
| 800 m | 0,00 |

#### Método cuadrático

```text
accesibilidad = (1 - distancia / radio)²
```

| Distancia | Accesibilidad cuadrática |
|---:|---:|
| 0 m | 1,00 |
| 200 m | 0,5625 |
| 400 m | 0,25 |
| 600 m | 0,0625 |
| 800 m | 0,00 |

El método cuadrático es más conservador con las zonas alejadas.

### 6.5. Pares de paradas

Para cada zona asignada a una parada de origen se buscan viajes hacia zonas
asignadas a paradas posteriores.

Ejemplo:

```text
Ruta: A → B → C

Pares evaluados:
A → B
A → C
B → C
```

Los movimientos contrarios no se suman porque pertenecen al otro sentido de
circulación.

### 6.6. Demanda bruta

La demanda bruta representa los viajes expandidos registrados entre las zonas
asignadas al par de paradas:

```text
demanda bruta =
    suma de viajes de la matriz origen-destino
```

Responde a:

> ¿Cuántos viajes existentes conectan las zonas cubiertas por ambas paradas?

### 6.7. Demanda potencial

La demanda potencial pondera cada viaje por la accesibilidad de ambos extremos:

```text
demanda potencial =
    viajes
    × accesibilidad del origen
    × accesibilidad del destino
```

Ejemplo:

```text
100 viajes brutos
accesibilidad de origen = 0,8
accesibilidad de destino = 0,5

demanda potencial = 100 × 0,8 × 0,5 = 40
```

La penalización en ambos extremos refleja que un desplazamiento requiere poder
acceder razonablemente tanto a la parada de subida como a la de bajada.

### 6.8. Interpretación

La demanda potencial no debe interpretarse automáticamente como pasajeros que
elegirán la línea.

No incluye todavía:

- Preferencia modal.
- Frecuencia del servicio.
- Transbordos.
- Competencia con otras líneas.
- Capacidad del vehículo.
- Elasticidad ante el tiempo de viaje.

El cálculo de recaudación (sección 10) aplica un factor de captación
configurable sobre esta demanda, pero ese factor es un supuesto de política,
no un modelo de elección de transporte.

Es una medida de demanda territorial accesible, útil para comparar rutas bajo
la misma metodología.

### 6.9. Ausencia de un par

Si no existen movimientos en la matriz entre las zonas asignadas, el par puede
no aparecer en el detalle. Su aporte al total es equivalente a cero.

## 7. Cálculo de distancia

### 7.1. Medición por tramo

Cada `LineString` se mide como una geometría geográfica sobre la superficie
terrestre.

La distancia no se calcula:

- Como línea recta entre paradas.
- Sumando distancias cartesianas en grados.
- Usando una distancia declarada manualmente.

Se sigue toda la geometría proporcionada.

### 7.2. Total

```text
distancia total =
    distancia A → B
  + distancia B → C
  + distancia C → D
```

El detalle por tramo permite:

- Detectar tramos desproporcionados.
- Calcular tiempos locales.
- Determinar la banda tarifaria de cada par de paradas (sección 10.3).
- Explicar el total.

### 7.3. Unidad

La salida usa metros para evitar pérdida de precisión:

```text
totalDistanceMeters
```

La presentación puede convertir el valor a kilómetros:

```text
kilómetros = metros / 1000
```

## 8. Estimación del tiempo de viaje

El tiempo se estima observando cómo están programadas las líneas existentes en
zonas similares.

No se aplica una única velocidad a todo el recorrido.

### 8.1. Por qué se calcula por tramo

La velocidad comercial cambia según:

- Tipo de vía.
- Densidad de intersecciones.
- Cantidad de detenciones.
- Configuración urbana.
- Características operativas de la zona.

Una ruta puede atravesar:

```text
zona rápida → zona céntrica lenta → corredor rápido
```

Calcular cada tramo por separado permite conservar esas diferencias.

### 8.2. Construcción previa de referencias GTFS

Antes de simular, el motor procesa los viajes programados de las líneas
existentes.

Para cada línea y sentido selecciona un viaje canónico que define:

- Secuencia principal de paradas.
- Geometría principal.
- Correspondencia entre paradas y recorrido.

Se prioriza:

1. El viaje con más paradas.
2. El de mayor duración.
3. Un desempate estable por identificador.

### 8.3. Viajes comparables

Para calcular el tiempo de un tramo existente solo se usan viajes cuya
secuencia completa de paradas coincide con la del viaje canónico.

Esto evita mezclar:

- Servicios cortos.
- Variantes.
- Recorridos parciales.
- Secuencias incompatibles.

### 8.4. Qué tiempo se mide entre paradas

En un tramo intermedio se mide:

```text
salida de la parada siguiente
menos
salida de la parada actual
```

Por lo tanto, el tiempo comercial incluye la detención programada en la parada
siguiente.

En el último tramo se utiliza:

```text
llegada a la parada final
menos
salida de la parada anterior
```

Así no se agrega una detención posterior a la finalización de la línea.

Los tiempos nulos o no positivos se descartan.

### 8.5. Tres escenarios

Para cada tramo existente se calculan percentiles:

| Escenario | Estadístico | Interpretación |
|---|---:|---|
| Valle | Percentil 25 | Referencia relativamente rápida. |
| Típico | Percentil 50 | Mediana programada. |
| Pico | Percentil 75 | Referencia relativamente lenta. |

Los nombres valle, típico y pico describen escenarios estadísticos.

No significan necesariamente:

- Una franja horaria de madrugada.
- Una hora pico etiquetada.
- Tránsito medido en esos momentos.

### 8.6. Búsqueda de referencias locales

Para cada tramo nuevo se buscan tramos de líneas existentes dentro de corredores
progresivos:

```text
100 m → 300 m → 800 m
```

También deben circular en una dirección compatible. La diferencia máxima
aceptada es 60 grados.

Un recorrido que pasa por la misma calle en sentido contrario no se considera
equivalente.

### 8.7. Control de valores anómalos

Se aceptan referencias cuya velocidad comercial típica esté entre:

```text
2 km/h y 80 km/h
```

El filtro elimina:

- Tiempos imposibles o casi nulos.
- Velocidades extremadamente bajas.
- Errores de horarios.
- Referencias incompatibles con transporte urbano.

### 8.8. Peso de cada referencia

Una referencia tiene más importancia cuando:

- Se superpone con una mayor parte del tramo nuevo.
- Está más cerca de la geometría propuesta.

Conceptualmente:

```text
peso =
    proporción de superposición
    × factor de cercanía
```

Dos tramos dentro del mismo corredor no tienen necesariamente el mismo peso.

### 8.9. Una línea, un voto consolidado

Una línea existente puede aportar varios tramos próximos.

Antes de combinar distintas líneas, el motor consolida todas las referencias de
una misma línea.

Esto evita que una línea con:

- Más paradas.
- Más fragmentos.
- Mayor densidad de segmentación.

tenga más influencia solo por la forma en que fue modelada.

### 8.10. Mediana entre líneas

Después de consolidar cada línea, se obtiene una mediana ponderada para cada
escenario.

La mediana reduce la influencia de líneas excepcionalmente rápidas o lentas.

El ritmo resultante se expresa en segundos por metro y se aplica a la distancia
del tramo nuevo:

```text
tiempo del tramo =
    distancia del tramo
    × segundos por metro estimados
```

### 8.11. Selección progresiva

```mermaid
flowchart TD
    START["Tramo nuevo"] --> R100{"¿Hay al menos<br/>3 líneas a 100 m?"}
    R100 -- Sí --> U100["Usar referencias de 100 m"]
    R100 -- No --> R300{"¿Hay al menos<br/>3 líneas a 300 m?"}
    R300 -- Sí --> U300["Usar referencias de 300 m"]
    R300 -- No --> R800{"¿Hay al menos<br/>3 líneas a 800 m?"}
    R800 -- Sí --> U800["Usar referencias de 800 m"]
    R800 -- No --> ANY{"¿Hay al menos<br/>1 línea a 800 m?"}
    ANY -- Sí --> UANY["Usar la muestra local reducida"]
    ANY -- No --> GLOBAL["Usar referencia global"]
```

Se usa siempre el primer corredor que cumple la condición. Esto prioriza
similitud territorial antes que cantidad adicional de datos lejanos.

Los corredores se miden de a uno y en orden, y cada ampliación se consulta
únicamente para los tramos que el corredor anterior no logró resolver. Medir un
corredor de 800 metros es mucho más costoso que uno de 100, y en la mayoría de
los recorridos casi todos los tramos quedan resueltos en el más angosto, así
que anticipar esa medición sería trabajo que después se descarta. La regla de
selección es la misma: sólo cambia cuándo se paga cada medición.

### 8.12. Respaldo global

Si no existe ninguna referencia local, el motor utiliza una mediana global de
las líneas GTFS válidas.

El respaldo:

- Permite completar la simulación.
- Evita inventar una velocidad arbitraria.
- Se identifica explícitamente en la salida.
- Siempre tiene confianza baja.

Si la base no contiene ninguna referencia GTFS válida, la estimación no puede
realizarse.

### 8.13. Totales

Cada tiempo de tramo se redondea a segundos enteros, con un mínimo de un
segundo.

Luego:

```text
tiempo total =
    suma de los tiempos de todos los tramos
```

El procedimiento se repite para valle, típico y pico.

## 9. Fuentes y confianza

### 9.1. Confianza por tramo

| Fuente | Cantidad de líneas | Confianza |
|---|---:|---|
| `local_100m` | 3 o más | Alta |
| `local_300m` | 3 o más | Media |
| `local_800m` | 3 o más | Media |
| `local_800m` | 1 o 2 | Baja |
| `global` | Respaldo general | Baja |

### 9.2. Por qué 100 metros tiene confianza alta

Las referencias están muy próximas al recorrido nuevo y existen al menos tres
líneas independientes.

Esto no garantiza exactitud futura, pero proporciona una base local
comparativamente sólida.

### 9.3. Por qué 300 y 800 metros tienen confianza media

Existe diversidad suficiente de líneas, pero las condiciones urbanas pueden
variar dentro de un corredor más amplio.

### 9.4. Muestras pequeñas

Una o dos líneas a 800 metros permiten una aproximación local, pero no alcanzan
para considerarla robusta.

### 9.5. Confianza total

La confianza total es la peor confianza entre todos los tramos.

Ejemplo:

```text
Tramo A → B: alta
Tramo B → C: alta
Tramo C → D: baja

Confianza total: baja
```

Esta regla evita ocultar un tramo débil dentro de un promedio general.

### 9.6. Interpretación correcta

La confianza describe la calidad y proximidad de las referencias disponibles.

No es:

- Una probabilidad de acertar.
- Un intervalo estadístico.
- Una garantía de puntualidad.
- Una medición de calidad del servicio.

## 10. Cálculo de recaudación potencial

La recaudación potencial estima qué ingresos por tarifa podría generar la
demanda potencial de la ruta, aplicando el cuadro tarifario de la jurisdicción
declarada y dos supuestos explícitos de comportamiento de pago.

### 10.1. Flujo

```mermaid
flowchart LR
    PAIRS["Demanda potencial<br/>por par de paradas"] --> DIST["Distancia acumulada<br/>del par"]
    DIST --> BAND["Banda tarifaria<br/>de la jurisdicción"]
    PAIRS --> CAPTURE["Demanda captada"]
    BAND --> FARE["Tarifa ponderada<br/>por mezcla de pago"]
    CAPTURE --> REVENUE["Recaudación<br/>potencial del par"]
    FARE --> REVENUE
    REVENUE --> TOTAL["Recaudación<br/>potencial total"]
```

### 10.2. Jurisdicción y cuadro tarifario

La jurisdicción declarada en la ruta (sección 4.1) selecciona qué cuadro
tarifario aplica.

El cuadro tarifario (`vialis.tarifas_colectivo`) define, para cada
jurisdicción, bandas de distancia con:

- Distancia mínima, inclusive.
- Distancia máxima, exclusiva. La última banda de cada jurisdicción no tiene
  máximo y cubre cualquier distancia mayor.
- Tarifa con tarjeta registrada.
- Tarifa sin registrar.

Los importes se almacenan en centavos para evitar errores de redondeo. El
cuadro cargado actualmente corresponde a las tarifas AMBA publicadas para
agosto de 2026 (`sql/tarifas/insertar_tarifas_vigentes.sql`). Actualizarlo es
un proceso administrado, igual que el resto de los datos de referencia
(sección 14).

### 10.3. Distancia del par

La distancia de un par de paradas es la suma de las distancias de los tramos
`PathToNext` entre la parada de origen y la de destino, ya calculadas para la
estimación de tiempo (sección 7). No se vuelve a medir la geometría.

### 10.4. Selección de banda

Se busca la banda cuya distancia mínima sea menor o igual a la distancia del
par y cuya distancia máxima sea mayor que esa distancia, o no exista. Si
ninguna banda cubre la distancia calculada, la simulación falla
explícitamente en lugar de aplicar una tarifa aproximada.

### 10.5. Demanda captada

```text
demanda captada = demanda potencial del par × factor de captación
```

El factor de captación (`SIMULATION_REVENUE_CAPTURE_FACTOR`, entre 0 y 1,
predeterminado 1) representa qué proporción de la demanda territorialmente
accesible se asume que efectivamente paga un viaje en la línea. Es un
supuesto de política configurable, no una estimación derivada de datos de
elección modal.

### 10.6. Mezcla de pago

```text
tarifa ponderada =
    tarifa registrada × proporción con tarjeta registrada
  + tarifa sin registrar × (1 − proporción con tarjeta registrada)
```

La proporción con tarjeta registrada (`SIMULATION_REGISTERED_CARD_SHARE`,
entre 0 y 1, predeterminado 1) refleja que buena parte de los boletos de
colectivo se paga con tarjeta registrada, a un valor distinto del de la
tarifa sin registrar.

### 10.7. Recaudación por par y total

```text
recaudación potencial del par = demanda captada × tarifa ponderada

recaudación potencial total =
    suma de la recaudación potencial de todos los pares
```

### 10.8. Interpretación

La recaudación potencial hereda las limitaciones de la demanda potencial
(sección 6.8): no incorpora evasión, elasticidad frente a la tarifa, ni
competencia con otras líneas más allá de lo que ya asume el factor de
captación. Es una cifra comparativa entre escenarios de simulación bajo los
mismos supuestos, no una proyección financiera.

## 11. Resultado de la simulación

El resultado tiene dos ejes: la ruta completa y el aporte de cada parada.

```text
Resultado
├── global
│   ├── Demanda
│   ├── Recaudación
│   └── Métricas
│       ├── Distancia total
│       └── Tiempo de viaje
└── byStop
    └── una entrada por parada
        ├── Demanda
        ├── Recaudación
        └── Tramo hacia la parada siguiente
```

### 11.1. Nivel de detalle

Internamente el motor calcula bastante más de lo que informa. La demanda y la
recaudación se derivan de cada par origen-destino, y la cantidad de pares crece
con el cuadrado de la cantidad de paradas: un recorrido de 139 paradas produce
más de 2.400 pares.

Ese detalle queda dentro del motor. Para decidir si una ruta vale la pena
alcanza con conocer la ruta en conjunto y la parte que le toca a cada parada,
que son exactamente los dos ejes de la salida.

### 11.2. Totales de la ruta

`global.demand` informa:

- `grossDemand`: demanda bruta total.
- `potentialDemand`: demanda potencial total.

`global.revenue` informa:

- `jurisdiction`: jurisdicción aplicada.
- `captureFactor` y `registeredCardShare`: supuestos de política vigentes al
  momento de la simulación.
- `potentialRevenueCents`: recaudación potencial total, en centavos.

`global.metrics` informa:

- `totalDistanceMeters`: suma de todas las geometrías.
- `travelTime`: los tres escenarios y la confianza total.

### 11.3. Aporte por parada

Cada entrada de `byStop` informa el orden y el identificador de la parada, más
tres bloques.

`demand` y `revenue` distinguen lo que **nace** en la parada de lo que **termina**
en ella:

| Campo | Significado |
|---|---|
| `demand.originGross` | Demanda bruta de los viajes que empiezan en la parada. |
| `demand.originPotential` | Lo mismo, ponderado por accesibilidad. |
| `demand.destinationGross` | Demanda bruta de los viajes que terminan en la parada. |
| `demand.destinationPotential` | Lo mismo, ponderado por accesibilidad. |
| `revenue.originPotentialCents` | Recaudación de los viajes que empiezan ahí. |
| `revenue.destinationPotentialCents` | Recaudación de los viajes que terminan ahí. |

Origen y destino se informan por separado a propósito. Cada par aporta a sus
dos paradas, así que sumar ambas columnas contaría cada viaje dos veces;
separadas, cada columna suma exactamente el total de la ruta:

```text
Σ originPotential = Σ destinationPotential = global.demand.potentialDemand
```

Además responden preguntas distintas: una parada donde la gente sube y una
donde baja justifican decisiones urbanísticas diferentes.

`segmentToNext` describe el viaje desde esa parada hasta la siguiente:
distancia, los tres tiempos, confianza, fuente y cantidad de líneas de
referencia. Es `null` en la última parada, igual que el `PathToNext` de la ruta
que se envía (sección 4.3).

Se informan **todas** las paradas, incluidas las que aportan cero. Mostrar que
una parada no aporta demanda es justamente lo que permite justificar
eliminarla.

### 11.4. Ejemplo de estructura

```json
{
  "global": {
    "demand": {
      "grossDemand": 175,
      "potentialDemand": 118
    },
    "revenue": {
      "jurisdiction": "caba",
      "captureFactor": 1,
      "registeredCardShare": 1,
      "potentialRevenueCents": 118000
    },
    "metrics": {
      "totalDistanceMeters": 1500,
      "travelTime": {
        "offPeakSeconds": 300,
        "typicalSeconds": 450,
        "peakSeconds": 600,
        "confidence": "medium"
      }
    }
  },
  "byStop": [
    {
      "stopOrder": 0,
      "stopId": "A",
      "demand": {
        "originGross": 150,
        "originPotential": 100,
        "destinationGross": 0,
        "destinationPotential": 0
      },
      "revenue": {
        "originPotentialCents": 100000,
        "destinationPotentialCents": 0
      },
      "segmentToNext": {
        "distanceMeters": 1000,
        "offPeakSeconds": 100,
        "typicalSeconds": 200,
        "peakSeconds": 300,
        "confidence": "high",
        "source": "local_100m",
        "referenceRouteCount": 3
      }
    }
  ]
}
```

Los números son ilustrativos y se muestra una sola parada.

### 11.5. Cómo leer el resultado

Una evaluación debería observar al menos:

1. Demanda potencial total.
2. Distribución de la demanda entre paradas, y si se concentra en pocas.
3. Paradas con aporte nulo o casi nulo.
4. Distancia total.
5. Tiempo típico total.
6. Diferencia entre valle y pico.
7. Tramos lentos.
8. Tramos con confianza baja.
9. Uso de respaldo global.
10. Recaudación potencial total y su distribución entre paradas.

El total por sí solo puede ocultar dónde se concentra la demanda o la
recaudación, o dónde la estimación es débil.

### 11.6. Una advertencia sobre el aporte por parada

El aporte de una parada **no** es lo que la ruta perdería si se la elimina.

Al quitar una parada, sus zonas H3 se reasignan a las paradas vecinas que
queden dentro del radio de acceso (sección 6.3), así que una parte de su
demanda se conserva. En un caso medido sobre una línea real, una parada que
aportaba 1.710 de demanda potencial de origen produjo una caída de 1.307 al
eliminarla: el resto pasó a sus vecinas.

Para saber el efecto real de una modificación hay que simular la ruta
modificada y comparar (sección 12).

## 12. Comparación de dos rutas

Modificar una línea existente —agregar paradas, eliminarlas o cambiar las
calles por las que circula— sólo tiene sentido si se puede ver el efecto de esa
modificación. Para eso el motor puede evaluar dos rutas y devolver ambos
resultados junto con la diferencia entre ellos.

### 12.1. Dos rutas completas, no un cambio declarado

La entrada son **dos rutas enteras**: una base y una propuesta. No se envía una
descripción de qué cambió.

El motor evalúa rutas, no las edita (sección 15.1). Si se enviara algo como
"eliminar la parada 8", el motor tendría que decidir por dónde pasa ahora el
recorrido entre las paradas 7 y 9, y esa decisión es de diseño, no de
evaluación: podría unir los dos tramos, tomar otra avenida, o evitar un giro
prohibido. Quien propone la modificación es quien sabe cuál corresponde.

Por eso **la geometría siempre la aporta quien consulta**. Al eliminar una
parada, el nuevo `PathToNext` del tramo resultante viene en la propuesta.

Ambas rutas se validan con las mismas reglas de la sección 5, sin excepción.

### 12.2. Misma jurisdicción

Las dos rutas deben declarar la misma jurisdicción. Comparar resultados
calculados bajo cuadros tarifarios distintos mezclaría el efecto de la
modificación con el de un cambio de tarifa, y la diferencia dejaría de ser
atribuible a la propuesta.

El resto de los supuestos —captación, mezcla de pago, método de accesibilidad—
son configuración del motor, así que ya son idénticos para ambas.

### 12.3. Qué se informa

```text
Comparación
├── baseline   ← resultado completo de la ruta base
├── proposed   ← resultado completo de la ruta propuesta
└── delta      ← cuánto se movió cada métrica
```

Los dos resultados son idénticos en estructura al de una simulación individual
(sección 11), así que el aporte por parada de cada versión está disponible para
ver de dónde viene la diferencia.

El `delta` informa, para cada métrica:

| Campo | Significado |
|---|---|
| `baseline` | Valor en la ruta base. |
| `proposed` | Valor en la ruta propuesta. |
| `absolute` | `proposed − baseline`. |
| `relative` | Fracción de la base: `0,12` significa doce por ciento más. |

Cubre cantidad de paradas, demanda bruta y potencial, recaudación potencial,
distancia total y los tres tiempos de viaje.

### 12.4. Diferencias relativas indefinidas

Cuando el valor de la ruta base es cero, `relative` se informa como nulo.

El cociente no está definido ahí, y presentarlo como crecimiento infinito sería
engañoso: agregar demanda a una ruta que no llevaba ninguna es una ganancia
absoluta, y sólo `absolute` la describe correctamente.

### 12.5. Confianza

La confianza no se resta. Se informan las dos etiquetas:

```json
{ "baseline": "high", "proposed": "medium" }
```

Alta, media y baja son categorías ordenadas, no cantidades: la distancia entre
dos de ellas no significa nada, así que restarlas produciría un número sin
interpretación. Que una modificación baje la confianza es información
relevante, y mostrar ambos valores es la forma honesta de comunicarlo.

### 12.6. Qué observar en una comparación

Un delta favorable en demanda no alcanza por sí solo. Conviene mirar además:

1. Si el tiempo de viaje creció, y cuánto respecto de la demanda ganada.
2. Si la confianza empeoró, lo que indicaría que la propuesta pasa por zonas
   con menos referencias.
3. Cómo se redistribuyó la demanda entre paradas, comparando el `byStop` de
   ambas versiones.
4. Si la distancia cambió, lo que además puede mover la banda tarifaria de
   algunos pares (sección 10.4).

### 12.7. Limitaciones

La comparación hereda todas las limitaciones de cada simulación (sección 16).
Además:

- Ambas rutas se evalúan contra el estado actual de la base. La comparación es
  válida entre sí, pero no es una serie histórica.
- No se persiste (sección 16.10): cada comparación se calcula en el momento.
- El motor no propone modificaciones ni sugiere qué parada conviene eliminar.
  Evalúa la propuesta que recibe.

## 13. Ejemplo conceptual

Supongamos una ruta:

```text
A → B → C
```

El tramo `A → B` recorre una avenida rápida. El tramo `B → C` atraviesa una
zona céntrica.

### 13.1. Demanda

| Par | Demanda bruta | Accesibilidad combinada | Demanda potencial |
|---|---:|---:|---:|
| A → B | 100 | 0,70 | 70 |
| A → C | 50 | 0,60 | 30 |
| B → C | 25 | 0,72 | 18 |
| **Total** | **175** | — | **118** |

### 13.2. Tiempo

| Tramo | Distancia | Ritmo típico | Tiempo típico | Fuente |
|---|---:|---:|---:|---|
| A → B | 1.000 m | 0,20 s/m | 200 s | 100 m |
| B → C | 500 m | 0,50 s/m | 250 s | 300 m |
| **Total** | **1.500 m** | — | **450 s** | — |

Una velocidad única habría ocultado que el segundo tramo es más lento.

### 13.3. Recaudación

Para simplificar, se asume una única banda tarifaria de 1.000 centavos, con
captación total y pago 100 % con tarjeta registrada (valores predeterminados):

| Par | Distancia | Demanda potencial | Tarifa ponderada | Recaudación potencial |
|---|---:|---:|---:|---:|
| A → B | 1.000 m | 70 | 1.000 centavos | 70.000 centavos |
| A → C | 1.500 m | 30 | 1.000 centavos | 30.000 centavos |
| B → C | 500 m | 18 | 1.000 centavos | 18.000 centavos |
| **Total** | — | **118** | — | **118.000 centavos** |

### 13.4. Aporte por parada

Los pares anteriores son el cálculo interno. Lo que el motor informa es el
aporte de cada parada (sección 11.3), que se obtiene agrupándolos:

| Parada | Demanda origen | Demanda destino | Recaudación origen | Recaudación destino | Tramo siguiente |
|---|---:|---:|---:|---:|---|
| A | 100 | 0 | 100.000 | 0 | 1.000 m, 200 s |
| B | 18 | 70 | 18.000 | 70.000 | 500 m, 250 s |
| C | 0 | 48 | 0 | 48.000 | — |
| **Total** | **118** | **118** | **118.000** | **118.000** | **1.500 m, 450 s** |

Las dos columnas de demanda suman 118 cada una, que es la demanda potencial
total. Sumarlas entre sí daría 236 y no correspondería a nada: cada par ya está
contado una vez como origen y otra como destino.

La parada A no recibe a nadie porque es la cabecera, y C no origina viajes
porque es la terminal. Ambas aparecen igual, con sus ceros.

### 13.5. Comparación de una modificación

Supongamos que se propone eliminar la parada B, uniendo los dos tramos en uno
solo de 1.500 metros. La ruta pasa a ser `A → C`.

El aporte de B era de 18 de demanda origen y 70 de destino, pero la ruta no
pierde 88: al desaparecer B, sus zonas H3 se reasignan a A y a C si quedan
dentro del radio de acceso, y parte de esa demanda se conserva con otra
accesibilidad. Con valores ilustrativos:

| Métrica | Base | Propuesta | Absoluto | Relativo |
|---|---:|---:|---:|---:|
| Paradas | 3 | 2 | −1 | −0,33 |
| Demanda potencial | 118 | 95 | −23 | −0,19 |
| Recaudación potencial | 118.000 | 95.000 | −23.000 | −0,19 |
| Distancia total | 1.500 m | 1.500 m | 0 | 0 |
| Tiempo típico | 450 s | 430 s | −20 | −0,04 |
| Confianza | media | media | — | — |

La distancia no cambia porque la geometría es la misma: sólo se dejó de
detener en el medio. El tiempo baja levemente al ahorrarse una detención. Y la
demanda cae 23, bastante menos que los 88 que B mostraba como aporte.

Esa diferencia entre "lo que la parada aportaba" y "lo que la ruta pierde" es
la razón por la que una modificación se evalúa simulando y comparando, y no
restando el aporte de la parada eliminada.

### 13.6. Interpretación

El escenario original sugiere:

- Una demanda potencial de 118 viajes representativos.
- Un recorrido de 1,5 km.
- Un tiempo típico de 7 minutos y 30 segundos.
- Una recaudación potencial de 118.000 centavos, bajo captación total y pago
  íntegramente con tarjeta registrada.
- Mejor respaldo en el primer tramo que en el segundo.

La modificación evaluada reduce la demanda un 19 % a cambio de 20 segundos
menos de recorrido, sin cambiar la distancia ni la confianza. Si ese
intercambio conviene depende de criterios que el motor no evalúa.

No permite concluir todavía:

- Cuántos pasajeros elegirían efectivamente la línea.
- Cuántos vehículos serían necesarios.

## 14. Preparación y actualización de datos

El motor necesita datos preparados antes de simular.

### 14.1. Flujo de movilidad

```mermaid
flowchart LR
    CSV["Viajes"] --> POINTS["Orígenes y destinos<br/>geográficos"]
    POINTS --> H3["Celdas H3"]
    H3 --> HOT["Puntos de máxima<br/>concurrencia"]
    H3 --> OD["Matriz<br/>origen-destino"]
    HOT --> SIM["Simulación"]
    OD --> SIM
```

La actualización de viajes modifica:

- Zonas disponibles.
- Puntos de máxima concurrencia.
- Cantidades de la matriz.
- Demanda estimada de futuras simulaciones.

### 14.2. Flujo GTFS

```mermaid
flowchart LR
    FEED["Feed GTFS"] --> RAW["Tablas temporales"]
    RAW --> VALID["Validación"]
    VALID --> CANON["Viajes canónicos"]
    CANON --> SEG["Recorridos y tramos"]
    RAW --> TIMES["Percentiles de tiempo"]
    TIMES --> SEG
    SEG --> SIM["Simulación"]
```

Una actualización GTFS puede modificar:

- Geometrías existentes.
- Secuencias de paradas.
- Distancias.
- Tiempos de referencia.
- Cantidad de líneas disponibles en cada corredor.
- Confianza de una misma ruta simulada.

### 14.3. Flujo de tarifas

```mermaid
flowchart LR
    PUBLICADA["Cuadro tarifario<br/>publicado"] --> CARGA["Carga administrada<br/>por jurisdicción"]
    CARGA --> TARIFAS["vialis.tarifas_colectivo"]
    TARIFAS --> SIM["Simulación"]
```

Una actualización tarifaria modifica directamente la recaudación potencial de
simulaciones futuras, sin afectar demanda ni tiempo de viaje. No existe un
proceso automatizado de sincronización con la fuente oficial: la carga se
realiza mediante un script SQL versionado
(`sql/tarifas/insertar_tarifas_vigentes.sql`) que debe actualizarse
manualmente cuando cambia el cuadro publicado.

### 14.4. Naturaleza de las actualizaciones

La preparación no ocurre dentro de cada simulación. Es un proceso previo
administrado.

Esto permite respuestas más rápidas, pero requiere:

- Registrar qué versión de los datos está activa.
- Actualizar viajes, GTFS y tarifas con una frecuencia definida.
- Validar las cargas antes de reemplazar datos productivos.

### 14.5. Reconstrucción GTFS

La transformación GTFS actual reconstruye las tablas finales de recorridos y
paradas.

Antes de ejecutarla se debe:

- Confirmar que las tablas temporales GTFS estén pobladas.
- Respaldar datos manuales asociados a recorridos.
- Considerar que los identificadores internos pueden cambiar.

Los datos de viajes, matriz OD y tarifas no se modifican durante esa
reconstrucción.

### 14.6. Estado operativo actual

El repositorio incluye los procesos de transformación, pero algunos pasos de
carga de archivos se realizan externamente.

Además:

- La importación de etapas individuales no está implementada.
- Algunos procesos de viajes requieren limpieza antes de repetirse.
- El cuadro tarifario no se versiona automáticamente; reemplazarlo requiere
  actualizar el script de carga.
- Las simulaciones no se persisten.
- No existe todavía un historial de versiones de datos y resultados.

## 15. Decisiones funcionales

### 15.1. Evaluar una ruta, no diseñarla

El motor no propone automáticamente paradas o calles. Esto mantiene separadas:

- Generación de alternativas.
- Evaluación de alternativas.

### 15.2. Un sentido por simulación

La demanda y el recorrido son direccionales. Ida y vuelta deben evaluarse por
separado si sus paradas o geometrías difieren.

### 15.3. Demanda territorial

La demanda se vincula con áreas cercanas a paradas y no exclusivamente con
puntos exactos.

Esto es apropiado para analizar cobertura, aunque no reemplaza un modelo de
elección de transporte.

### 15.4. Asignación exclusiva de zonas

Evita doble contabilización, pero hace que la distribución por parada dependa
de la configuración completa de paradas.

### 15.5. Recorrido exacto

Permite medir distancia y condiciones locales sin depender de un servicio
externo de mapas.

### 15.6. Tiempo local antes que promedio global

Se priorizan referencias cercanas. El promedio global se utiliza únicamente
como respaldo.

### 15.7. Variabilidad explícita

El motor devuelve tres escenarios en lugar de un único tiempo. Esto comunica
que la operación no tiene una duración constante.

### 15.8. Procedencia visible

Fuente y confianza forman parte del resultado para que una cifra estimada no se
presente sin contexto.

### 15.9. Fallar ante geometrías inválidas

Una corrección automática podría evaluar una ruta distinta. Por eso la entrada
se rechaza y debe corregirse en origen.

### 15.10. Jurisdicción como entrada explícita

La jurisdicción tarifaria se declara en la ruta y no se infiere de las
coordenadas. Inferirla automáticamente podría aplicar una tarifa incorrecta en
zonas limítrofes sin que quede en evidencia.

### 15.11. Captación y mezcla de pago como supuestos de política

El factor de captación y la proporción de tarjeta registrada son parámetros de
configuración, no resultados calibrados con datos observados de la línea. Se
tratan igual que la política de accesibilidad (sección 6.4): valores
explícitos, documentados y versionables, no un modelo de elección de
transporte.

### 15.12. Comparar dos rutas completas, no un cambio declarado

Una modificación se expresa enviando la ruta resultante entera, no una
instrucción de qué cambiar. Es consecuencia directa de la decisión 15.1: si el
motor tuviera que interpretar "eliminar esta parada", tendría que decidir por
dónde pasa ahora el recorrido, y eso es diseñar. Quien propone el cambio es
quien sabe si corresponde unir los dos tramos, tomar otra calle o evitar un
giro prohibido.

### 15.13. Un resultado del tamaño de la decisión

El motor calcula el detalle de cada par origen-destino pero no lo informa. La
cantidad de pares crece con el cuadrado de la cantidad de paradas, y ese
detalle no es lo que se mira para decidir si una ruta conviene.

La salida se organiza entonces en los dos ejes que sí sostienen una decisión:
la ruta completa y el aporte de cada parada (sección 11). Devolver menos
también es lo que hace práctico entregar dos resultados completos en una
comparación.

### 15.14. Diferencias relativas indefinidas en lugar de infinitas

Cuando una métrica vale cero en la ruta base, la diferencia relativa se informa
como nula y no como un crecimiento enorme. Un cociente indefinido presentado
como número invita a leerlo como una mejora espectacular cuando en realidad
sólo indica que antes no había nada que comparar.

### 15.15. La confianza no se resta

Alta, media y baja son categorías ordenadas, no cantidades. Una comparación
informa ambas etiquetas en lugar de su diferencia, porque la distancia entre
dos niveles no tiene interpretación (sección 12.5).

## 16. Alcance actual y limitaciones

### 16.1. Demanda y recaudación potencial no equivalen a captación real

La demanda potencial indica movimientos accesibles, no pasajeros asegurados.
La recaudación potencial (sección 10) aplica un factor de captación
configurable sobre esa demanda, pero ese factor es un supuesto de política,
no una estimación calibrada con datos de elección modal.

Faltan variables como:

- Frecuencia del servicio.
- Tiempo de espera.
- Transbordos.
- Competidores.
- Preferencias de los usuarios.
- Evasión y elasticidad frente a la tarifa.

### 16.2. Día típico

Los datos de movilidad representan un día hábil típico. No describen:

- Fines de semana.
- Eventos.
- Estacionalidad.
- Cambios recientes no incluidos en la carga.

### 16.3. Cobertura geográfica

La fuente de viajes tiene alcance AMBA. El alcance efectivo depende del archivo
cargado y no de un recorte automático del motor.

### 16.4. Accesibilidad simplificada

La distancia es geográfica y no peatonal. El modelo no conoce:

- Ríos o autopistas como barreras.
- Pasos habilitados.
- Calidad urbana.
- Seguridad.

### 16.5. Búsqueda H3 acotada

La búsqueda de demanda parte de la vecindad H3 inmediata de la parada y después
aplica el radio de 800 metros. Esta estrategia es eficiente, pero debería
revisarse si se cambia la resolución H3 o el radio.

### 16.6. Horarios programados

Los tiempos GTFS no observan:

- Demoras reales.
- Congestión.
- Cancelaciones.
- Incumplimientos.
- Variabilidad diaria no programada.

### 16.7. Dirección aproximada

La compatibilidad usa la orientación general entre extremos del tramo. En
geometrías muy curvas, la dirección local puede estar representada de forma
simplificada.

### 16.8. Cantidad de muestras

Se conserva cuántos viajes GTFS contribuyeron a cada percentil, pero esa
cantidad no aumenta automáticamente el peso de una línea durante la
simulación.

### 16.9. Cuadro tarifario sin versionado histórico

La simulación usa siempre el cuadro tarifario vigente en la base de datos al
momento de ejecutarse. No conserva tarifas históricas ni permite simular con
la tarifa de una fecha pasada.

### 16.10. Resultado no persistido

Actualmente no se guarda:

- Entrada.
- Resultado.
- Fecha.
- Versión de datos.
- Parámetros.

Dos ejecuciones en momentos distintos podrían cambiar después de actualizar la
base, sin que el motor conserve por sí mismo la comparación histórica.

### 16.11. Exposición actual

El servicio HTTP publica una comprobación de salud, la simulación de una ruta y
la comparación de dos rutas. El contrato está documentado en
`docs/openapi.yaml`. También existe una herramienta de línea de comandos que
simula una ruta desde un archivo JSON.

Todavía no se expone un catálogo de las líneas existentes: quien consulta debe
construir la ruta base por su cuenta, aunque corresponda a una línea que el
motor ya tiene cargada.

### 16.12. Cálculo sincrónico

Cada consulta se resuelve dentro del mismo pedido, contra un límite de tiempo
configurable. Las dos simulaciones de una comparación se calculan en paralelo,
así que una comparación tarda aproximadamente lo mismo que la más lenta de las
dos rutas, no la suma.

La mayoría de los recorridos del AMBA se resuelven en menos de un segundo, pero
unos pocos recorridos suburbanos muy extensos —de 90 a 130 kilómetros—
requieren bastante más, porque cada uno de sus tramos atraviesa muchos
corredores existentes. Para esos casos el límite de tiempo puede quedar corto y
la respuesta ser un error de tiempo agotado.

## 17. Evolución prevista

### 17.1. Modelo de captación calibrado

El factor de captación y la mezcla de pago son actualmente parámetros fijos de
configuración (secciones 10.5 y 10.6). Pueden complementarse con un modelo que
estime captación a partir de:

- Frecuencia propuesta.
- Tiempo de espera.
- Tiempo frente a alternativas.
- Cantidad de transbordos.

Esto permitiría reemplazar el supuesto fijo por una proporción específica de
cada ruta y jurisdicción.

### 17.2. Comparación con líneas similares

Una línea existente puede considerarse similar según:

- Distancia total.
- Superposición.
- Zonas cubiertas.
- Cantidad de paradas.
- Tiempo típico.
- Demanda.
- Recaudación.

La comparación debería explicar qué criterios originaron la similitud.

### 17.3. Datos observados

Tiempos GPS o AVL podrían incorporarse con una jerarquía de fuentes:

```text
observación local
→ horario local
→ horario global
```

La salida debería mantener la procedencia.

### 17.4. Persistencia y escenarios

Guardar las simulaciones permitiría:

- Comparar alternativas.
- Reproducir resultados.
- Auditar cambios.
- Asociar cada cálculo con una versión de datos.

### 17.5. Versionado histórico de tarifas

Guardar cada cuadro tarifario con su fecha de vigencia permitiría simular con
la tarifa vigente en una fecha pasada y auditar variaciones de recaudación
potencial causadas exclusivamente por actualizaciones tarifarias.

### 17.6. Publicación mediante API

El mismo contrato puede exponerse a una aplicación web sin cambiar el
funcionamiento conceptual.

También conviene separar:

- Salud del proceso.
- Disponibilidad de la base.
- Vigencia de los datos.

## 18. Glosario

| Término | Significado |
|---|---|
| Accesibilidad | Coeficiente que reduce el aporte de una zona según su distancia a la parada. |
| Aporte por parada | Demanda y recaudación atribuidas a una parada, separadas según los viajes que nacen y los que terminan en ella. |
| Banda tarifaria | Rango de distancia con una tarifa registrada y una tarifa sin registrar definidas para una jurisdicción. |
| Demanda bruta | Viajes de la matriz entre zonas cubiertas, antes de ponderar accesibilidad. |
| Demanda captada | Demanda potencial de un par de paradas multiplicada por el factor de captación. |
| Demanda potencial | Demanda bruta ponderada por accesibilidad en origen y destino. |
| Delta | Diferencia entre la ruta propuesta y la ruta base, con su valor absoluto y su fracción relativa. |
| Factor de captación | Proporción configurable de la demanda potencial que se asume paga un viaje en la línea. |
| Factor de expansión | Peso estadístico que convierte una fila de muestra en viajes representados. |
| GTFS | Formato estándar de oferta, recorridos, paradas y horarios de transporte. |
| H3 | Sistema de indexación geográfica mediante celdas hexagonales. |
| Jurisdicción | Autoridad tarifaria aplicable a la ruta simulada (`caba`, `province` o `national`). |
| LineString | Geometría ordenada que representa un recorrido. |
| Matriz OD | Cantidad de viajes entre zonas de origen y destino. |
| Percentil 25 | Valor por debajo del cual queda el 25 % de los tiempos. |
| Percentil 50 | Mediana de los tiempos. |
| Percentil 75 | Valor por debajo del cual queda el 75 % de los tiempos. |
| Recaudación potencial | Ingreso estimado por tarifa a partir de la demanda captada y la tarifa ponderada de cada par de paradas. |
| Ritmo comercial | Segundos necesarios por metro recorrido, incluyendo detenciones según el criterio GTFS. |
| Ruta base | Ruta contra la que se compara una propuesta. En una modificación, la línea tal como existe hoy. |
| Tarifa ponderada | Combinación de la tarifa registrada y la tarifa sin registrar según la proporción de tarjeta registrada asumida. |
| Tramo | Recorrido desde una parada hasta la siguiente. |
| Viaje canónico | Viaje GTFS elegido para representar la secuencia principal de una línea y sentido. |

## 19. Conclusión

Vialis Motor evalúa una ruta nueva combinando movilidad existente, oferta de
transporte programada, geometría detallada y el cuadro tarifario de la
jurisdicción declarada.

Su funcionamiento se apoya en tres cálculos complementarios:

- La demanda determina qué movimientos territoriales podrían ser atendidos.
- La distancia y el tiempo describen el comportamiento operativo esperado.
- La recaudación potencial traduce esa demanda en un ingreso estimado, bajo
  supuestos explícitos de captación y mezcla de pago.

El resultado se organiza en dos ejes: la ruta completa y el aporte de cada
parada, con el tramo que la conecta con la siguiente. Esto permite identificar
no solo cuánta demanda, tiempo o recaudación tiene una propuesta, sino dónde se
originan esos valores y qué tan sólidas son las referencias utilizadas.

Sobre esa misma base, el motor puede evaluar dos rutas y devolver la diferencia
entre ellas (sección 12). Así una modificación de una línea existente —agregar
o quitar paradas, cambiar las calles que recorre— deja de ser un resultado
aislado y pasa a poder leerse como impacto: cuánta demanda gana o pierde,
cuánto tiempo agrega y si la estimación se vuelve menos confiable.

Las métricas actuales constituyen una base para completar la evaluación de
viabilidad con costos, comparación automática contra líneas similares y un
modelo de captación calibrado, sin perder la trazabilidad del cálculo.
