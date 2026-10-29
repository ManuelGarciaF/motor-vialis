package postgres

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestDemoLatestAnalyzedCaptureIntegration(t *testing.T) {
	f := newRealtimeFixture(t, time.Minute)
	f.migrate("migrar_realtime.sql", "migrar_aglutinaciones.sql")
	r := &BunchingRepository{database: f.tx, schema: f.schema}
	if _, err := r.LatestAnalyzedCaptureAt(f.ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("empty demo: %v", err)
	}
	at := time.Date(2020, 1, 2, 3, 4, 5, 123000, time.UTC)
	for i := 0; i < 3; i++ {
		_, err := f.tx.Exec(f.ctx, r.sql(`
			INSERT INTO vialis.capturas_colectivos (
			    recibido_en, filas_totales, filas_validas, filas_invalidas,
			    filas_duplicadas, filas_stale, cobertura_parcial, aglutinaciones_analizadas
			) VALUES ($1, 0, 0, 0, 0, 0, false, $2)
		`), at.Add(time.Duration(i-1)*time.Minute), i < 2)
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.LatestAnalyzedCaptureAt(f.ctx)
	if err != nil || !got.Equal(at) {
		t.Fatalf("latest analyzed = %v, %v; want %v", got, err, at)
	}
}
