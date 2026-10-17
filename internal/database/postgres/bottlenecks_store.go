package postgres

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed bottlenecks_materialize_stops.sql
var bottlenecksMaterializeStopsSQL string

// BottlenecksStore persists derived evidence. It never runs migrations, captures
// GPS, rematches raw, or activates a worker loop implicitly.
type BottlenecksStore struct {
	database realtimeBeginner
	schema   string
}

func NewBottlenecksStore(pool *pgxpool.Pool) (*BottlenecksStore, error) {
	if pool == nil {
		return nil, fmt.Errorf("bottleneck database is required")
	}
	return &BottlenecksStore{database: pool, schema: "vialis"}, nil
}

func (s *BottlenecksStore) sql(query string) string { return queryInSchema(query, s.schema) }

func rollbackBottlenecks(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = tx.Rollback(ctx)
}

func (s *BottlenecksStore) readRun(ctx context.Context, tx pgx.Tx, name string, lock bool) (bottlenecks.ProcessingRun, error) {
	var r bottlenecks.ProcessingRun
	var config []byte
	query := `
		SELECT
		    configuracion,
		    activado_en,
		    captura_inicial,
		    checkpoint,
		    ultima_captura_recibida_en
		FROM vialis.h0012_modelos
		WHERE nombre = $1
	`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, s.sql(query), name).Scan(&config, &r.ActivatedAt, &r.ActivationCaptureID, &r.Checkpoint, &r.LastReceivedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return r, bottlenecks.ErrModelNotFound
	}
	if err != nil {
		return r, err
	}
	if err = json.Unmarshal(config, &r.Model); err != nil {
		return r, err
	}
	if err = r.Model.Validate(); err != nil {
		return r, err
	}
	if r.Model.Name != name || r.Checkpoint < r.ActivationCaptureID {
		return r, fmt.Errorf("invalid persisted measurement run")
	}
	return r, nil
}

// Activate takes the ingestion advisory lock only for the initial watermark.
// This includes in-flight H0011 inserts before selecting max(id), avoiding an
// earlier allocated capture ID becoming visible after activation. Restarting an
// existing model never changes activation or checkpoint, even after raw purge.
func (s *BottlenecksStore) Activate(ctx context.Context, model bottlenecks.PersistenceModel) (bottlenecks.ProcessingRun, error) {
	var empty bottlenecks.ProcessingRun
	if err := model.Validate(); err != nil {
		return empty, err
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	if _, err = tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock($1)
	`, realtimeLock); err != nil {
		return empty, err
	}

	existing, err := s.readRun(ctx, tx, model.Name, true)
	if err == nil {
		if existing.Model != model {
			return empty, bottlenecks.ErrModelChanged
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return existing, nil
	}
	if !errors.Is(err, bottlenecks.ErrModelNotFound) {
		return empty, err
	}
	// A table lock also covers manual imports not using H0011's advisory lock.
	if _, err = tx.Exec(ctx, s.sql(`
		LOCK TABLE vialis.capturas_colectivos IN SHARE MODE
	`)); err != nil {
		return empty, err
	}

	config, err := json.Marshal(model)
	if err != nil {
		return empty, err
	}

	_, err = tx.Exec(ctx, s.sql(`
		INSERT INTO vialis.h0012_modelos (
		    nombre,
		    configuracion,
		    captura_inicial,
		    checkpoint
		)
		SELECT
		    $1,
		    $2,
		    coalesce(max(id_captura), 0),
		    coalesce(max(id_captura), 0)
		FROM vialis.capturas_colectivos
	`), model.Name, config)
	if err != nil {
		return empty, err
	}

	run, err := s.readRun(ctx, tx, model.Name, false)
	if err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return run, nil
}

func (s *BottlenecksStore) readLayout(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, id int64) (bottlenecks.PersistedLayout, error) {
	var v bottlenecks.ShapeVersion
	var length float64
	err := tx.QueryRow(ctx, s.sql(`
		SELECT
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    gtfs_shape_hash,
		    longitud_metros
		FROM vialis.h0012_trazados
		WHERE modelo = $1
		    AND id = $2
	`), run.Model.Name, id).Scan(&v.RouteID, &v.Direction, &v.ShapeID, &v.ShapeHash, &length)
	if err != nil {
		return bottlenecks.PersistedLayout{}, err
	}

	layout, err := bottlenecks.NewLayout(v, length, run.Model.NominalMeters)
	return bottlenecks.PersistedLayout{ID: id, Layout: layout}, err
}

// RegisterLayout freezes full geometry, sectors and ordered stops in one
// transaction. Candidate stop/sector associations preserve ambiguity; they are
// not a decision about which branch of a loop actually serves the stop.
func (s *BottlenecksStore) RegisterLayout(ctx context.Context, model string, lineID int64) (bottlenecks.PersistedLayout, error) {
	var empty bottlenecks.PersistedLayout
	if lineID <= 0 {
		return empty, fmt.Errorf("invalid line ID")
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return empty, err
	}
	// Prevent catalog replacement during geometry/stop snapshot materialization.
	if _, err = tx.Exec(ctx, s.sql(`
		LOCK TABLE vialis.recorridos,
		vialis.recorridos_paradas,
		vialis.paradas IN SHARE MODE
	`)); err != nil {
		return empty, err
	}

	var version bottlenecks.ShapeVersion
	var length float64
	var geom []byte
	var line, branch string
	err = tx.QueryRow(ctx, s.sql(`
		SELECT
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    md5(encode(ST_AsEWKB(geom), 'hex')),
		    ST_Length(geom::geography),
		    ST_AsEWKB(geom),
		    linea,
		    ramal
		FROM vialis.recorridos
		WHERE id_recorrido = $1
	`), lineID).Scan(&version.RouteID, &version.Direction, &version.ShapeID, &version.ShapeHash, &length, &geom, &line, &branch)
	if errors.Is(err, pgx.ErrNoRows) {
		return empty, bottlenecks.ErrLineNotFound
	}
	if err != nil {
		return empty, err
	}

	layout, err := bottlenecks.NewLayout(version, length, run.Model.NominalMeters)
	if err != nil {
		return empty, err
	}

	var id int64
	err = tx.QueryRow(ctx, s.sql(`
		INSERT INTO vialis.h0012_trazados (
		    modelo,
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    gtfs_shape_hash,
		    linea,
		    ramal,
		    longitud_metros,
		    geom,
		    paradas
		)
		VALUES (
		    $1,
		    $2,
		    $3,
		    $4,
		    $5,
		    $6,
		    $7,
		    $8,
		    ST_GeomFromEWKB($9),
		    '[]'
		)
		ON CONFLICT (
		    modelo,
		    gtfs_route_id,
		    direction_id,
		    gtfs_shape_id,
		    gtfs_shape_hash
		)
		DO NOTHING
		RETURNING id
	`), model, version.RouteID, version.Direction, version.ShapeID, version.ShapeHash, line, branch, layout.LengthMeters(), geom).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, s.sql(`
			SELECT id
			FROM vialis.h0012_trazados
			WHERE modelo = $1
			    AND gtfs_route_id = $2
			    AND direction_id = $3
			    AND gtfs_shape_id = $4
			    AND gtfs_shape_hash = $5
		`), model, version.RouteID, version.Direction, version.ShapeID, version.ShapeHash).Scan(&id)
		if err != nil {
			return empty, err
		}

		persisted, err := s.readLayout(ctx, tx, run, id)
		if err != nil {
			return empty, err
		}
		if err = tx.Commit(ctx); err != nil {
			return empty, err
		}
		return persisted, nil
	}
	if err != nil {
		return empty, err
	}

	sectors := layout.Sectors()
	for _, sector := range sectors {
		start, end := sector.StartMeters/length, sector.EndMeters/length
		if sector.Index == len(sectors)-1 {
			end = 1
		}
		_, err = tx.Exec(ctx, s.sql(`
			INSERT INTO vialis.h0012_sectores (
			    trazado,
			    indice,
			    inicio_metros,
			    fin_metros,
			    geom
			)
			SELECT
			    id,
			    $2,
			    $3,
			    $4,
			    ST_LineSubstring(geom::geography, $5, $6)::geometry
			FROM vialis.h0012_trazados
			WHERE id = $1
		`), id, sector.Index, sector.StartMeters, sector.EndMeters, start, end)
		if err != nil {
			return empty, err
		}
	}

	var stops []byte
	if err = tx.QueryRow(ctx, s.sql(bottlenecksMaterializeStopsSQL), id, lineID, run.Model.Policy.MaximumDistanceMeters).Scan(&stops); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, s.sql(`
		UPDATE vialis.h0012_trazados
		SET paradas = $2
		WHERE id = $1
	`), id, stops); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return bottlenecks.PersistedLayout{ID: id, Layout: layout}, nil
}

func (s *BottlenecksStore) loadStates(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun) ([]bottlenecks.PersistedStream, error) {
	rows, err := tx.Query(ctx, s.sql(`
		SELECT
		    trazado,
		    vehiculo,
		    checkpoint,
		    estado
		FROM vialis.h0012_estados
		WHERE modelo = $1
		ORDER BY trazado,
		vehiculo
	`), run.Model.Name)
	if err != nil {
		return nil, err
	}

	streams := []bottlenecks.PersistedStream{}
	for rows.Next() {
		var stream bottlenecks.PersistedStream
		var vehicle string
		var checkpoint int64
		var state []byte
		if err = rows.Scan(&stream.LayoutID, &vehicle, &checkpoint, &state); err != nil {
			break
		}
		if err = json.Unmarshal(state, &stream.State); err != nil {
			break
		}
		if stream.State.VehicleID != vehicle || stream.State.LastCaptureID != checkpoint || checkpoint != run.Checkpoint {
			err = fmt.Errorf("inconsistent stream checkpoint")
			break
		}

		streams = append(streams, stream)
	}

	rowErr := rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if rowErr != nil {
		return nil, rowErr
	}

	layouts := make(map[int64]bottlenecks.PersistedLayout)
	for _, stream := range streams {
		layout, exists := layouts[stream.LayoutID]
		if !exists {
			layout, err = s.readLayout(ctx, tx, run, stream.LayoutID)
			if err != nil {
				return nil, err
			}

			layouts[stream.LayoutID] = layout
		}
		if stream.State.Policy != run.Model.Policy {
			return nil, bottlenecks.ErrModelChanged
		}
		if err = stream.State.Validate(layout.Layout); err != nil {
			return nil, err
		}
	}

	return streams, nil
}

func (s *BottlenecksStore) LoadStates(ctx context.Context, model string) ([]bottlenecks.PersistedStream, error) {
	tx, err := s.database.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer rollbackBottlenecks(tx)
	// Match model checkpoint and all states from one committed writer version.
	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return nil, err
	}

	streams, err := s.loadStates(ctx, tx, run)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return streams, nil
}

// CommitCapture accepts domain transitions, not caller-supplied API records.
// All streams, results, diagnostics and checkpoint commit together. The row
// lock serializes processors of the same model without holding H0011's lock.
func (s *BottlenecksStore) CommitCapture(ctx context.Context, model string, expected, captureID int64, transitions []bottlenecks.StreamTransition) (bottlenecks.CaptureCommit, error) {
	empty := bottlenecks.CaptureCommit{}
	if expected < 0 || captureID <= 0 {
		return empty, fmt.Errorf("invalid capture commit")
	}

	tx, err := s.database.Begin(ctx)
	if err != nil {
		return empty, err
	}
	defer rollbackBottlenecks(tx)

	run, err := s.readRun(ctx, tx, model, true)
	if err != nil {
		return empty, err
	}
	if captureID <= run.Checkpoint {
		return empty, bottlenecks.ErrCaptureAlreadyProcessed
	}
	if expected != run.Checkpoint {
		return empty, bottlenecks.ErrCheckpointConflict
	}

	reference := run.ActivatedAt
	if run.LastReceivedAt != nil {
		reference = *run.LastReceivedAt
	}
	if err = s.checkRetention(ctx, tx, reference, run.Model.RawRetentionDays); err != nil {
		return empty, err
	}

	var nextID int64
	var received time.Time
	var partial bool
	err = tx.QueryRow(ctx, s.sql(`
		SELECT
		    id_captura,
		    recibido_en,
		    cobertura_parcial
		FROM vialis.capturas_colectivos
		WHERE id_captura > $1
		ORDER BY id_captura
		LIMIT 1
		FOR SHARE
	`), expected).Scan(&nextID, &received, &partial)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && nextID != captureID {
		return empty, bottlenecks.ErrCaptureOutOfOrder
	}
	if err != nil {
		return empty, err
	}
	if run.LastReceivedAt != nil && received.Before(*run.LastReceivedAt) {
		return empty, fmt.Errorf("capture receipt regressed")
	}
	if err = s.checkRetention(ctx, tx, received, run.Model.RawRetentionDays); err != nil {
		return empty, err
	}

	streams, err := s.loadStates(ctx, tx, run)
	if err != nil {
		return empty, err
	}

	type streamKey struct {
		layout  int64
		vehicle string
	}
	existing := make(map[streamKey]bottlenecks.CaptureState, len(streams))
	for _, stream := range streams {
		existing[streamKey{stream.LayoutID, stream.State.VehicleID}] = stream.State
	}

	seen := make(map[streamKey]bool, len(transitions))
	layouts := make(map[int64]bottlenecks.PersistedLayout)
	result := bottlenecks.CaptureCommit{Checkpoint: captureID, Rejections: make(map[bottlenecks.Rejection]int)}
	for _, transition := range transitions {
		state := transition.State
		key := streamKey{transition.LayoutID, state.VehicleID}
		if seen[key] {
			return empty, fmt.Errorf("duplicate stream transition")
		}

		seen[key] = true
		old, exists := existing[key]
		if exists && transition.PreviousCaptureID != old.LastCaptureID || !exists && transition.PreviousCaptureID != 0 {
			return empty, bottlenecks.ErrCheckpointConflict
		}
		if state.LastCaptureID != captureID || !state.LastReceivedAt.Equal(received) || state.Policy != run.Model.Policy {
			return empty, fmt.Errorf("transition capture/model mismatch")
		}

		layout, ok := layouts[transition.LayoutID]
		if !ok {
			layout, err = s.readLayout(ctx, tx, run, transition.LayoutID)
			if err != nil {
				return empty, err
			}

			layouts[transition.LayoutID] = layout
		}
		if err = state.Validate(layout.Layout); err != nil {
			return empty, err
		}
		if err = validateStoredMeasurement(layout.Layout, state, transition.Measurement); err != nil {
			return empty, err
		}
		if exists && old.Watermark != nil && (state.Watermark == nil || state.Watermark.ObservedAt.Before(old.Watermark.ObservedAt)) {
			return empty, fmt.Errorf("GPS watermark regressed")
		}
		if exists && state.ClosedThrough.Before(old.ClosedThrough) {
			return empty, fmt.Errorf("closed watermark regressed")
		}
		if err = validateStoredAbsence(old, exists, state, transition.Measurement, partial); err != nil {
			return empty, err
		}

		for _, p := range transition.Measurement.Passages {
			if err = s.insertMeasurement(ctx, tx, run, transition.LayoutID, captureID, p.VehicleID, p.Scope, p.Sector, p.Entered, true, "", p.Duration, p.SpeedKPH, p); err != nil {
				return empty, err
			}

			result.Passages++
		}

		for _, p := range transition.Measurement.Incomplete {
			if err = s.insertMeasurement(ctx, tx, run, transition.LayoutID, captureID, p.VehicleID, p.Scope, p.Sector, p.Entered, false, string(p.Reason), 0, 0, p); err != nil {
				return empty, err
			}

			result.Incomplete++
		}

		data, err := json.Marshal(state)
		if err != nil {
			return empty, err
		}

		_, err = tx.Exec(ctx, s.sql(`
			INSERT INTO vialis.h0012_estados (
			    modelo,
			    trazado,
			    vehiculo,
			    checkpoint,
			    estado
			)
			VALUES (
			    $1,
			    $2,
			    $3,
			    $4,
			    $5
			)
			ON CONFLICT (
			    modelo,
			    trazado,
			    vehiculo
			)
			DO UPDATE
			SET
			    checkpoint = EXCLUDED.checkpoint,
			    estado = EXCLUDED.estado
		`), model, transition.LayoutID, state.VehicleID, captureID, data)
		if err != nil {
			return empty, err
		}

		m := transition.Measurement
		result.Duplicates += m.Duplicates
		result.LateObservations += m.LateObservations
		if m.Closed {
			result.ClosedStreams++
		}
		for reason, count := range m.Rejections {
			result.Rejections[reason] += count
		}
	}

	for key := range existing {
		if !seen[key] {
			return empty, fmt.Errorf("capture commit omitted a tracked stream")
		}
	}

	diagnostic, err := json.Marshal(result)
	if err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, s.sql(`
		INSERT INTO vialis.h0012_diagnosticos (
		    modelo,
		    captura,
		    diagnostico
		)
		VALUES (
		    $1,
		    $2,
		    $3
		)
	`), model, captureID, diagnostic); err != nil {
		return empty, err
	}
	if _, err = tx.Exec(ctx, s.sql(`
		UPDATE vialis.h0012_modelos
		SET
		    checkpoint = $2,
		    ultima_captura_recibida_en = $3
		WHERE nombre = $1
	`), model, captureID, received); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}

// Metadata/ID gaps cannot prove whether purged captures once existed. Refuse to
// advance a stale run rather than treating missing source as empty observations.
func (s *BottlenecksStore) checkRetention(ctx context.Context, tx pgx.Tx, reference time.Time, days int) error {
	var expired bool
	err := tx.QueryRow(ctx, `
		SELECT($1::timestamptz AT TIME ZONE 'UTC')::date <(clock_timestamp() AT TIME ZONE 'UTC')::date -($2::integer - 1)
	`, reference, days).Scan(&expired)
	if err != nil {
		return err
	}
	if expired {
		return bottlenecks.ErrSourceExpired
	}
	return nil
}

func (s *BottlenecksStore) insertMeasurement(ctx context.Context, tx pgx.Tx, run bottlenecks.ProcessingRun, layoutID, captureID int64, vehicle string, scope bottlenecks.Scope, sector bottlenecks.Sector, entry bottlenecks.Crossing, complete bool, reason string, duration time.Duration, speed float64, payload any) error {
	// Canonical UTC nanosecond text prevents timestamptz's microsecond precision
	// from collapsing distinct entries. Completion/reason are not part of identity.
	key := struct {
		Layout  int64
		Vehicle string
		Scope   bottlenecks.Scope
		Sector  int
		Entry   string
	}{layoutID, vehicle, scope, sector.Index, entry.At.UTC().Format(time.RFC3339Nano)}
	identity, err := json.Marshal(key)
	if err != nil {
		return err
	}

	digest := sha256.Sum256(identity)
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	location, err := time.LoadLocation(run.Model.TimeZone)
	if err != nil {
		return err
	}

	local := entry.At.In(location)
	var why, ns, kph any
	if complete {
		ns = int64(duration)
		kph = speed
	} else {
		why = reason
	}
	_, err = tx.Exec(ctx, s.sql(`
		INSERT INTO vialis.h0012_mediciones (
		    modelo,
		    identidad,
		    trazado,
		    sector,
		    vehiculo,
		    gcba_route_id,
		    gcba_direction,
		    captura,
		    entrada_en,
		    fecha_local,
		    hora_local,
		    completa,
		    motivo,
		    duracion_ns,
		    velocidad_kph,
		    soporte
		)
		VALUES (
		    $1,
		    $2,
		    $3,
		    $4,
		    $5,
		    $6,
		    $7,
		    $8,
		    $9,
		    $10::date,
		    $11,
		    $12,
		    $13,
		    $14,
		    $15,
		    $16
		)
	`), run.Model.Name, hex.EncodeToString(digest[:]), layoutID, sector.Index, vehicle, scope.ProviderRouteID, scope.ProviderDirection, captureID, entry.At, local.Format("2006-01-02"), local.Hour(), complete, why, ns, kph, data)
	return err
}

// Capture completeness comes from H0011 metadata, not a producer-supplied flag.
func validateStoredAbsence(old bottlenecks.CaptureState, exists bool, next bottlenecks.CaptureState, measurement bottlenecks.CaptureMeasurement, partial bool) error {
	if !exists {
		if next.Watermark == nil || next.MissingCaptures != 0 || measurement.Closed || !next.ClosedThrough.IsZero() {
			return fmt.Errorf("invalid new stream")
		}
		return nil
	}

	updated := next.Watermark != nil && (old.Watermark == nil || next.Watermark.ObservedAt.After(old.Watermark.ObservedAt))
	expected := old.MissingCaptures
	closed := false
	closedThrough := old.ClosedThrough
	if updated {
		expected = 0
	} else if !partial && old.Watermark != nil && expected < bottlenecks.MissingCaptureLimit {
		expected++
		closed = expected == bottlenecks.MissingCaptureLimit
		if closed {
			closedThrough = old.Watermark.ObservedAt
		}
	}
	if next.MissingCaptures != expected || measurement.Closed != closed || !next.ClosedThrough.Equal(closedThrough) {
		return fmt.Errorf("transition disagrees with capture absence metadata")
	}
	return nil
}

func validateStoredMeasurement(layout bottlenecks.Layout, state bottlenecks.CaptureState, m bottlenecks.CaptureMeasurement) error {
	if m.Duplicates < 0 || m.LateObservations < 0 {
		return fmt.Errorf("invalid measurement counters")
	}

	for _, count := range m.Rejections {
		if count < 0 {
			return fmt.Errorf("invalid rejection counter")
		}
	}

	sectors := layout.Sectors()
	validate := func(vehicle string, scope bottlenecks.Scope, sector bottlenecks.Sector, c bottlenecks.Crossing) error {
		if vehicle != state.VehicleID ||
			scope.Version != layout.Version() ||
			scope.ProviderRouteID == "" ||
			scope.ProviderDirection < 0 ||
			scope.ProviderDirection > 1 ||
			sector.Index < 0 ||
			sector.Index >= len(sectors) ||
			sectors[sector.Index] != sector {
			return fmt.Errorf("measurement identity/layout mismatch")
		}
		if c.At.IsZero() ||
			c.BeforeAt.IsZero() ||
			c.AfterAt.IsZero() ||
			c.At.Before(c.BeforeAt) ||
			c.At.After(c.AfterAt) ||
			c.AfterAt.Sub(c.BeforeAt) > state.Policy.MaximumGap ||
			state.Watermark == nil ||
			c.AfterAt.After(state.Watermark.ObservedAt) {
			return fmt.Errorf("invalid crossing support")
		}
		return nil
	}
	for _, p := range m.Passages {
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Entered); err != nil {
			return err
		}
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Exited); err != nil {
			return err
		}

		expected := p.Sector.LengthMeters() / p.Duration.Seconds() * 3.6
		if p.Duration <= 0 ||
			p.Duration != p.Exited.At.Sub(p.Entered.At) ||
			p.SpeedKPH != expected ||
			p.InteriorObservations < 0 ||
			p.MaximumSupportingGap < 0 ||
			p.MaximumSupportingGap > state.Policy.MaximumGap ||
			p.MaximumSupportingGap < p.Entered.AfterAt.Sub(p.Entered.BeforeAt) ||
			p.MaximumSupportingGap < p.Exited.AfterAt.Sub(p.Exited.BeforeAt) {
			return fmt.Errorf("invalid passage quality")
		}

		same := p.Entered.BeforeAt.Equal(p.Exited.BeforeAt) && p.Entered.AfterAt.Equal(p.Exited.AfterAt)
		if p.SameGPSInterval != same {
			return fmt.Errorf("invalid passage support category")
		}
	}

	for _, p := range m.Incomplete {
		if err := validate(p.VehicleID, p.Scope, p.Sector, p.Entered); err != nil {
			return err
		}
		if p.Reason == "" || p.Reason == bottlenecks.BatchBoundary {
			return fmt.Errorf("incremental entry has no final rejection reason")
		}
	}

	if m.Closed && (state.MissingCaptures != bottlenecks.MissingCaptureLimit || state.Previous != nil || state.Pending != nil) {
		return fmt.Errorf("invalid stream closure")
	}
	return nil
}

var _ bottlenecks.PassageStore = (*BottlenecksStore)(nil)
