# Imagen de base de datos de Vialis.
#
# Existe una imagen propia por un único motivo: `postgis/postgis:15-3.3` no trae
# la extensión H3, y el modelo de demanda no funciona sin ella (`vialis.viajes`
# indexa origen y destino en celdas H3 de resolución 8, y `sql/init_db.sql`
# ejecuta `create extension h3`). Instalarla a mano dentro de un contenedor ya
# en ejecución la deja en su capa de escritura: se pierde al recrearlo y la base
# deja de ser reproducible. Instalarla acá la vuelve parte de la imagen.
#
# `postgresql-15-h3` arrastra `libh3-1`; ambos provienen de los repositorios
# PGDG que la imagen de base ya tiene configurados.
FROM postgis/postgis:15-3.3

RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-15-h3 \
    && rm -rf /var/lib/apt/lists/*
