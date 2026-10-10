package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestBunchingLegacyMigration(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	schema := fmt.Sprintf("bunching_legacy_%d", time.Now().UnixNano())
	if _, err = tx.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	sql := (&RealtimeRepository{schema: schema}).sql
	_, err = tx.Exec(ctx, sql(`CREATE TABLE vialis.capturas_colectivos(id_captura bigint PRIMARY KEY,recibido_en timestamptz,filas_insertadas integer);
 CREATE TABLE vialis.posiciones_colectivos_raw(id_vehiculo text,id_captura bigint REFERENCES vialis.capturas_colectivos);
 CREATE TABLE vialis.aglutinaciones_colectivos(id_evento bigint PRIMARY KEY,gtfs_route_id text,gtfs_direction_id smallint,gtfs_shape_hash text,id_vehiculo_a text,id_vehiculo_b text,primero_observado_en timestamptz,ultimo_observado_en timestamptz,cerrado_en timestamptz,observaciones integer);
 CREATE UNIQUE INDEX idx_aglutinaciones_colectivos_abiertas ON vialis.aglutinaciones_colectivos(gtfs_route_id,gtfs_direction_id,id_vehiculo_a,id_vehiculo_b) WHERE cerrado_en IS NULL;
 INSERT INTO vialis.capturas_colectivos VALUES(1,'2026-09-30 22:31:00+00',1);
 INSERT INTO vialis.posiciones_colectivos_raw VALUES('legacy',1);
 INSERT INTO vialis.aglutinaciones_colectivos VALUES(1,'gtfs',0,'old-hash','a','b','2026-09-30 22:29:00+00','2026-09-30 22:30:00+00',NULL,2);`))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../../sql/recorridos/migrar_aglutinaciones.sql")
	if err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(string(data), "\\set ON_ERROR_STOP on", "")
	script = strings.ReplaceAll(script, "BEGIN;", "")
	script = strings.ReplaceAll(script, "COMMIT;", "")
	for range 2 {
		if _, err = tx.Exec(ctx, sql(script)); err != nil {
			t.Fatal(err)
		}
	}
	var partial, analyzed bool
	var fingerprint string
	var inserted, raw int
	err = tx.QueryRow(ctx, sql(`SELECT cobertura_parcial,aglutinaciones_analizadas,hash_posiciones,filas_insertadas FROM vialis.capturas_colectivos WHERE id_captura=1`)).Scan(&partial, &analyzed, &fingerprint, &inserted)
	if err != nil || !partial || analyzed || fingerprint != "" || inserted != 1 {
		t.Fatalf("legacy metadata: partial=%v analyzed=%v inserted=%d err=%v", partial, analyzed, inserted, err)
	}
	if err = tx.QueryRow(ctx, sql(`SELECT count(*) FROM vialis.posiciones_colectivos_raw`)).Scan(&raw); err != nil || raw != 1 {
		t.Fatalf("raw changed: %d %v", raw, err)
	}
	var events int
	if err = tx.QueryRow(ctx, sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos WHERE id_evento=1 AND gtfs_shape_hash='old-hash' AND observaciones=2`)).Scan(&events); err != nil || events != 1 {
		t.Fatalf("legacy event changed: %d %v", events, err)
	}
}
