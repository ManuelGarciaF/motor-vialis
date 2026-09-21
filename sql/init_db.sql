-- Crea el esquema de Vialis y activa las extensiones que usa el modelo.
--
-- Todo es idempotente a propósito: la imagen de `docker-compose.yml` ya trae
-- PostGIS activo en la base `vialis`, y `go run ./cmd/initdb --reset` vuelve a
-- ejecutar este script sobre una base donde las extensiones ya existen.
create schema if not exists vialis;

-- activar extensiones
create extension if not exists h3;
create extension if not exists postgis;
create extension if not exists h3_postgis cascade;
