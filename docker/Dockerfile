# Use a released PostGIS build. Installing Debian extension packages on top of
# the development "master" image can mix its source-built SQL files with
# packaged shared libraries, producing missing-symbol errors at CREATE EXTENSION.
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
