-- $1: esquema de las particiones de posiciones raw.
SELECT child.relname
FROM pg_inherits i
JOIN pg_class child ON child.oid = i.inhrelid
JOIN pg_namespace ns ON ns.oid = child.relnamespace
JOIN pg_class parent ON parent.oid = i.inhparent
WHERE ns.nspname = $1
  AND parent.relname = 'posiciones_colectivos_raw';
