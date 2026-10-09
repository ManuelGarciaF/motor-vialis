package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

// EnableBunching must be called before the repository is shared with a worker.
func (r *RealtimeRepository) EnableBunching(policy bunching.Policy) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	r.bunchingPolicy = &policy
	return nil
}

const eventColumns = `id_evento,gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,gtfs_shape_id,gtfs_shape_hash,id_vehiculo_a,id_vehiculo_b,primero_observado_en,ultimo_observado_en,COALESCE(observado_a_en,ultimo_observado_en),COALESCE(observado_b_en,ultimo_observado_en),cerrado_en,COALESCE(motivo_cierre,''),distancia_minima_metros,ultima_distancia_metros,ST_X(ultima_posicion_a),ST_Y(ultima_posicion_a),ST_X(ultima_posicion_b),ST_Y(ultima_posicion_b),observaciones,capturas_faltantes,linea,ramal,COALESCE(ultima_confirmacion_en,ultimo_observado_en)`

type eventScanner interface{ Scan(...any) error }

func scanEvent(row eventScanner, extra ...any) (bunching.Event, error) {
	var e bunching.Event
	args := []any{&e.ID, &e.Scope.GCBARouteID, &e.Scope.GCBADirection, &e.Scope.GTFSRouteID, &e.Scope.GTFSDirection, &e.Scope.ShapeID, &e.Scope.ShapeHash, &e.VehicleA, &e.VehicleB, &e.FirstObservedAt, &e.LastObservedAt, &e.ObservedA, &e.ObservedB, &e.ClosedAt, &e.CloseReason, &e.MinimumDistanceMeters, &e.LastDistanceMeters, &e.PointA.Longitude, &e.PointA.Latitude, &e.PointB.Longitude, &e.PointB.Latitude, &e.Observations, &e.MissingCaptures, &e.Line, &e.Branch, &e.ConfirmedAt}
	err := row.Scan(append(args, extra...)...)
	return e, err
}

func (r *RealtimeRepository) advanceBunching(ctx context.Context, tx pgx.Tx, capture bunching.Capture, result *bunching.StoreResult) error {
	policy := *r.bunchingPolicy
	var latest *time.Time
	if err := tx.QueryRow(ctx, r.sql(`SELECT max(recibido_en) FROM vialis.capturas_colectivos WHERE aglutinaciones_analizadas`)).Scan(&latest); err != nil {
		return err
	}
	// Late/replayed captures cannot move the operational clock backwards.
	if latest != nil && !capture.ReceivedAt.After(*latest) {
		result.BunchingSkipped = true
		return nil
	}
	result.BunchingSkipped = capture.Partial
	fingerprint := bunching.Fingerprint(capture.Positions)
	if !capture.Partial {
		var previousFingerprint string
		err := tx.QueryRow(ctx, r.sql(`SELECT hash_posiciones FROM vialis.capturas_colectivos WHERE aglutinaciones_analizadas AND NOT cobertura_parcial ORDER BY recibido_en DESC,id_captura DESC LIMIT 1`)).Scan(&previousFingerprint)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(ctx, r.sql(`CREATE TEMP TABLE bunching_snapshot ON COMMIT DROP AS
 WITH targets AS MATERIALIZED (
 SELECT e.gcba_route_id,e.gcba_direction,e.gtfs_route_id,e.gtfs_direction_id,r.gtfs_shape_id,r.linea,r.ramal,md5(encode(ST_AsEWKB(r.geom),'hex')) AS shape_hash
 FROM (SELECT DISTINCT route_id,direction FROM realtime_positions_batch) p
 JOIN vialis.equivalencias_recorridos_gcba e ON e.gcba_route_id=p.route_id AND e.gcba_direction=p.direction
 JOIN vialis.recorridos r ON r.gtfs_route_id=e.gtfs_route_id AND r.direction_id=e.gtfs_direction_id
 ), latest AS (
 SELECT DISTINCT ON(vehiculo COLLATE "C") * FROM realtime_positions_batch
 ORDER BY vehiculo COLLATE "C",observado DESC,route_id COLLATE "C",direction
 )
 SELECT p.vehiculo,p.observado,p.route_id,p.direction,e.gtfs_route_id,e.gtfs_direction_id,e.gtfs_shape_id,e.shape_hash,e.linea,e.ramal,raw.posicion
 FROM latest p JOIN targets e ON e.gcba_route_id=p.route_id AND e.gcba_direction=p.direction
 JOIN vialis.posiciones_colectivos_raw raw ON raw.dia_observado=p.dia AND raw.id_vehiculo=p.vehiculo AND raw.observado_en=p.observado
 WHERE p.observado>=$1 AND p.observado<=$2`), capture.ReceivedAt.Add(-policy.MaximumAge), capture.ReceivedAt.Add(policy.MaximumFutureSkew))
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `CREATE INDEX ON bunching_snapshot USING GIST((posicion::geography)); CREATE UNIQUE INDEX ON bunching_snapshot(vehiculo); ANALYZE bunching_snapshot`); err != nil {
			return err
		}
		snapshot := bunching.Snapshot{ReceivedAt: capture.ReceivedAt, HasNewPositions: fingerprint != previousFingerprint, Positions: map[string]bunching.Observation{}, Distances: map[bunching.PairKey]float64{}}
		rows, err := tx.Query(ctx, `SELECT vehiculo,observado,route_id,direction,gtfs_route_id,gtfs_direction_id,gtfs_shape_id,shape_hash,ST_X(posicion),ST_Y(posicion),linea,ramal FROM bunching_snapshot`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var o bunching.Observation
			if err = rows.Scan(&o.VehicleID, &o.ObservedAt, &o.Scope.GCBARouteID, &o.Scope.GCBADirection, &o.Scope.GTFSRouteID, &o.Scope.GTFSDirection, &o.Scope.ShapeID, &o.Scope.ShapeHash, &o.Point.Longitude, &o.Point.Latitude, &o.Line, &o.Branch); err != nil {
				rows.Close()
				return err
			}
			snapshot.Positions[o.VehicleID] = o
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		rows, err = tx.Query(ctx, r.sql(`SELECT `+eventColumns+` FROM vialis.aglutinaciones_colectivos WHERE cerrado_en IS NULL ORDER BY id_evento`))
		if err != nil {
			return err
		}
		var open []bunching.Event
		for rows.Next() {
			e, err := scanEvent(rows)
			if err != nil {
				rows.Close()
				return err
			}
			open = append(open, e)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// Expand only the index prefilter by a micrometre: ST_Project/ST_DWithin
		// can disagree at exactly 200m due to inverse-geodesic precision. The
		// domain still applies the strict <=200m ST_Distance result.
		rows, err = tx.Query(ctx, r.sql(`WITH candidates AS (
 SELECT a.vehiculo AS a,b.vehiculo AS b FROM bunching_snapshot a JOIN bunching_snapshot b
 ON a.vehiculo COLLATE "C"<b.vehiculo COLLATE "C" AND a.route_id=b.route_id AND a.direction=b.direction AND a.gtfs_route_id=b.gtfs_route_id AND a.gtfs_direction_id=b.gtfs_direction_id AND a.shape_hash=b.shape_hash
 AND ST_DWithin(a.posicion::geography,b.posicion::geography,$1)
 UNION
 SELECT e.id_vehiculo_a,e.id_vehiculo_b FROM vialis.aglutinaciones_colectivos e
 JOIN bunching_snapshot a ON a.vehiculo=e.id_vehiculo_a JOIN bunching_snapshot b ON b.vehiculo=e.id_vehiculo_b
 WHERE e.cerrado_en IS NULL AND a.route_id=e.gcba_route_id AND b.route_id=e.gcba_route_id AND a.direction=e.gcba_direction AND b.direction=e.gcba_direction AND a.gtfs_route_id=e.gtfs_route_id AND b.gtfs_route_id=e.gtfs_route_id AND a.gtfs_direction_id=e.gtfs_direction_id AND b.gtfs_direction_id=e.gtfs_direction_id AND a.shape_hash=e.gtfs_shape_hash AND b.shape_hash=e.gtfs_shape_hash
 ) SELECT a.route_id,a.direction,a.gtfs_route_id,a.gtfs_direction_id,a.gtfs_shape_id,a.shape_hash,c.a,c.b,ST_Distance(a.posicion::geography,b.posicion::geography)
 FROM candidates c JOIN bunching_snapshot a ON a.vehiculo=c.a JOIN bunching_snapshot b ON b.vehiculo=c.b`), policy.DistanceMeters+0.000001)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key bunching.PairKey
			var distance float64
			if err = rows.Scan(&key.Scope.GCBARouteID, &key.Scope.GCBADirection, &key.Scope.GTFSRouteID, &key.Scope.GTFSDirection, &key.Scope.ShapeID, &key.Scope.ShapeHash, &key.A, &key.B, &distance); err != nil {
				rows.Close()
				return err
			}
			snapshot.Distances[key] = distance
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if snapshot.HasNewPositions {
			snapshot.Previous = make(map[bunching.PairKey]bunching.PreviousSample)
			rows, err = tx.Query(ctx, r.sql(`SELECT `+eventColumns+` FROM vialis.aglutinaciones_colectivos WHERE cerrado_en IS NOT NULL AND ultimo_observado_en >= $1 ORDER BY id_evento DESC`), capture.ReceivedAt.Add(-policy.MaximumAge))
			if err != nil {
				return err
			}
			for rows.Next() {
				e, scanErr := scanEvent(rows)
				if scanErr != nil {
					rows.Close()
					return scanErr
				}
				key := bunching.Key(e.Scope, e.VehicleA, e.VehicleB)
				if _, exists := snapshot.Previous[key]; !exists {
					snapshot.Previous[key] = bunching.PreviousSample{ObservedA: e.ObservedA, ObservedB: e.ObservedB}
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
		}
		changes, err := bunching.Advance(open, snapshot, policy)
		if err != nil {
			return err
		}
		batch := &pgx.Batch{}
		for _, event := range changes {
			queueEvent(batch, r, event)
			if event.ID == 0 {
				result.EventsOpened++
			} else if event.ClosedAt != nil {
				result.EventsClosed++
			} else {
				result.EventsUpdated++
			}
		}
		results := tx.SendBatch(ctx, batch)
		if err = results.Close(); err != nil {
			return fmt.Errorf("persist bunching changes: %w", err)
		}
		if _, err = tx.Exec(ctx, `DROP TABLE bunching_snapshot`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(ctx, r.sql(`UPDATE vialis.capturas_colectivos SET aglutinaciones_analizadas=TRUE,hash_posiciones=$2 WHERE id_captura=$1`), result.CaptureID, fingerprint)
	result.BunchingProcessed = err == nil
	return err
}

func queueEvent(batch *pgx.Batch, r *RealtimeRepository, e bunching.Event) {
	args := []any{e.Scope.GCBARouteID, e.Scope.GCBADirection, e.Scope.GTFSRouteID, e.Scope.GTFSDirection, e.Scope.ShapeID, e.Scope.ShapeHash, e.VehicleA, e.VehicleB, e.FirstObservedAt, e.LastObservedAt, e.ObservedA, e.ObservedB, e.ClosedAt, e.CloseReason, e.MinimumDistanceMeters, e.LastDistanceMeters, e.PointA.Longitude, e.PointA.Latitude, e.PointB.Longitude, e.PointB.Latitude, e.Observations, e.MissingCaptures, e.Line, e.Branch, e.ConfirmedAt}
	// Empty closure reasons are SQL NULL, not an invalid enum value.
	if e.CloseReason == "" {
		args[13] = nil
	}
	if e.ID == 0 {
		batch.Queue(r.sql(`INSERT INTO vialis.aglutinaciones_colectivos(gcba_route_id,gcba_direction,gtfs_route_id,gtfs_direction_id,gtfs_shape_id,gtfs_shape_hash,id_vehiculo_a,id_vehiculo_b,primero_observado_en,ultimo_observado_en,observado_a_en,observado_b_en,cerrado_en,motivo_cierre,distancia_minima_metros,ultima_distancia_metros,ultima_posicion_a,ultima_posicion_b,observaciones,capturas_faltantes,linea,ramal,ultima_confirmacion_en)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,ST_SetSRID(ST_MakePoint($17,$18),4326),ST_SetSRID(ST_MakePoint($19,$20),4326),$21,$22,$23,$24,$25)`), args...)
	} else {
		args = append(args, e.ID)
		batch.Queue(r.sql(`UPDATE vialis.aglutinaciones_colectivos SET ultimo_observado_en=$10,observado_a_en=$11,observado_b_en=$12,cerrado_en=$13,motivo_cierre=$14,distancia_minima_metros=$15,ultima_distancia_metros=$16,ultima_posicion_a=ST_SetSRID(ST_MakePoint($17,$18),4326),ultima_posicion_b=ST_SetSRID(ST_MakePoint($19,$20),4326),observaciones=$21,capturas_faltantes=$22,ultima_confirmacion_en=$25 WHERE id_evento=$26
 AND gcba_route_id=$1 AND gcba_direction=$2 AND gtfs_route_id=$3 AND gtfs_direction_id=$4 AND gtfs_shape_id=$5 AND gtfs_shape_hash=$6 AND id_vehiculo_a=$7 AND id_vehiculo_b=$8 AND primero_observado_en=$9 AND linea=$23 AND ramal=$24`), args...)
	}
}
