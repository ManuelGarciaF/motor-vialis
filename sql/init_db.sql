-- Prepara el esquema y las extensiones dentro de la base ya creada.
--
-- Es re-ejecutable: el pipeline lo corre en cada ingesta para garantizar sus
-- precondiciones sin depender de que alguien lo haya ejecutado antes.
\set ON_ERROR_STOP on

CREATE SCHEMA IF NOT EXISTS vialis;

CREATE EXTENSION IF NOT EXISTS postgis;
CREATE EXTENSION IF NOT EXISTS h3;
CREATE EXTENSION IF NOT EXISTS h3_postgis CASCADE;
