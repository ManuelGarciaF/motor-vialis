# NiFi con cliente de PostgreSQL.
#
# El pipeline carga las tablas raw con COPY y ejecuta los scripts de
# transformación con psql, así que la imagen oficial necesita el cliente.
# Ver docs/pipeline_nifi.md para el porqué de COPY en lugar de JDBC.
FROM apache/nifi:1.28.1

USER root

RUN apt-get update \
    && apt-get install -y --no-install-recommends postgresql-client \
    && rm -rf /var/lib/apt/lists/*

USER nifi
