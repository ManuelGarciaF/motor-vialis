-- Toma un lock de aviso para serializar la preparación de la base.
--
-- Las tres ramas del pipeline ejecutan `preparar_base.sh` como primer paso y
-- pueden arrancar a la vez. `CREATE DATABASE` no tiene forma de decir
-- IF NOT EXISTS, y `CREATE EXTENSION IF NOT EXISTS` o `CREATE TABLE IF NOT
-- EXISTS` tampoco son atómicos frente a otra sesión haciendo lo mismo: la
-- verificación y la creación son dos pasos, y dos sesiones simultáneas fallan
-- con un error de clave duplicada sobre los catálogos.
--
-- El lock es a nivel de sesión y se libera solo cuando psql termina, así que
-- cubre todos los scripts que se ejecuten después en la misma invocación.
-- Los locks de aviso tienen alcance por base de datos, de modo que este mismo
-- archivo sirve tanto para la sesión sobre `postgres` que crea la base como
-- para la sesión sobre `vialis` que crea el esquema y las tablas.
\set ON_ERROR_STOP on

SELECT pg_advisory_lock(5219731);
