-- Crea la base de datos si todavía no existe.
--
-- Se ejecuta conectado a otra base (normalmente `postgres`), porque
-- CREATE DATABASE no puede correr dentro de la base que se está creando ni
-- dentro de una transacción. PostgreSQL no ofrece CREATE DATABASE IF NOT
-- EXISTS, así que la sentencia se genera solo cuando hace falta y se ejecuta
-- con \gexec.
--
-- El nombre se recibe como variable de psql:
--     psql -d postgres -v nombre_base=vialis -f sql/crear_base.sql
\set ON_ERROR_STOP on
\if :{?nombre_base}
\else
\set nombre_base vialis
\endif

SELECT format('CREATE DATABASE %I', :'nombre_base')
WHERE NOT EXISTS (
    SELECT 1
    FROM pg_database
    WHERE datname = :'nombre_base'
)
\gexec
