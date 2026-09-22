# Imagen reproducible con las extensiones que no incluye postgis/postgis.
# H3 alimenta el modelo de demanda y pgRouting habilita RF05.
ARG POSTGIS_IMAGE=postgis/postgis:18-3.6
FROM ${POSTGIS_IMAGE}

# hadolint ignore=DL3008
RUN apt-get update \
    && apt-get install --no-install-recommends --yes \
        "postgresql-${PG_MAJOR}-h3" \
        "postgresql-${PG_MAJOR}-pgrouting" \
    && test -f "/usr/share/postgresql/${PG_MAJOR}/extension/h3.control" \
    && test -f "/usr/share/postgresql/${PG_MAJOR}/extension/h3_postgis.control" \
    && test -f "/usr/share/postgresql/${PG_MAJOR}/extension/pgrouting.control" \
    && rm -rf /var/lib/apt/lists/*
