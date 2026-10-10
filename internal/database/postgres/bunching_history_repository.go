package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ManuelGarciaF/vialis-motor/internal/bunching"
	"github.com/jackc/pgx/v5"
)

func (r *BunchingRepository) FindHistory(ctx context.Context, q bunching.HistoryRequest, now time.Time, policy bunching.Policy) (bunching.EventPage, error) {
	page := bunching.EventPage{From: q.From, To: q.To, State: q.State, Limit: q.Limit, Offset: q.Offset, Stale: true, Partial: true, Events: []bunching.HistoricalEvent{}}
	tx, err := r.database.Begin(ctx)
	if err != nil {
		return page, err
	}
	defer tx.Rollback(context.Background())
	sql := (&RealtimeRepository{schema: r.schema}).sql
	err = tx.QueryRow(ctx, sql(`SELECT recibido_en,cobertura_parcial FROM vialis.capturas_colectivos WHERE aglutinaciones_analizadas ORDER BY recibido_en DESC,id_captura DESC LIMIT 1`)).Scan(&page.LatestCaptureAt, &page.Partial)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return page, err
	}
	if page.LatestCaptureAt != nil {
		age := now.Sub(*page.LatestCaptureAt).Seconds()
		if age < 0 {
			age = 0
		}
		page.CaptureAgeSeconds = &age
		page.Stale = now.Sub(*page.LatestCaptureAt) > policy.MaximumAge || page.LatestCaptureAt.After(now.Add(policy.MaximumFutureSkew))
	}
	// Intersects the lifecycle interval with [from,to). Open events end at the
	// last confirmation for filtering, not infinity; confirmed duration remains
	// last-first even when the lifecycle ends later due to missing evidence.
	where := ` WHERE observaciones >= $1 AND EXTRACT(EPOCH FROM (ultimo_observado_en-primero_observado_en)) >= $5::double precision AND primero_observado_en < $2 AND COALESCE(cerrado_en,ultimo_observado_en) >= $3 AND ($4='all' OR ($4='open' AND cerrado_en IS NULL) OR ($4='closed' AND cerrado_en IS NOT NULL))`
	args := []any{policy.MinimumObservations, q.To, q.From, q.State, policy.MinimumDuration.Seconds()}
	if err = tx.QueryRow(ctx, sql(`SELECT count(*) FROM vialis.aglutinaciones_colectivos`+where), args...).Scan(&page.Total); err != nil {
		return page, err
	}
	args = append(args, q.Limit, q.Offset)
	rows, err := tx.Query(ctx, sql(`SELECT `+eventColumns+`,(SELECT r.id_recorrido FROM vialis.recorridos r WHERE r.gtfs_route_id=e.gtfs_route_id AND r.direction_id=e.gtfs_direction_id AND md5(encode(ST_AsEWKB(r.geom),'hex'))=e.gtfs_shape_hash AND EXISTS(SELECT 1 FROM vialis.equivalencias_recorridos_gcba m WHERE m.gcba_route_id=e.gcba_route_id AND m.gcba_direction=e.gcba_direction AND m.gtfs_route_id=e.gtfs_route_id AND m.gtfs_direction_id=e.gtfs_direction_id)) AS current_line_id FROM vialis.aglutinaciones_colectivos e`+where+` ORDER BY primero_observado_en DESC,id_evento DESC LIMIT $6 OFFSET $7`), args...)
	if err != nil {
		return page, err
	}
	for rows.Next() {
		var lineID *int64
		e, err := scanEvent(rows, &lineID)
		if err != nil {
			rows.Close()
			return page, err
		}
		state := "open"
		if e.ClosedAt != nil {
			state = "closed"
		}
		active := e.ClosedAt == nil && lineID != nil && !page.Partial && !page.Stale && e.MissingCaptures == 0 && !e.ConfirmedAt.Before(now.Add(-policy.MaximumAge)) && !e.ObservedA.Before(now.Add(-policy.MaximumAge)) && !e.ObservedB.Before(now.Add(-policy.MaximumAge)) && !e.ObservedA.After(now.Add(policy.MaximumFutureSkew)) && !e.ObservedB.After(now.Add(policy.MaximumFutureSkew))
		page.Events = append(page.Events, bunching.HistoricalEvent{Event: e, LineID: lineID, State: state, Active: active, ClosedAt: e.ClosedAt, CloseReason: e.CloseReason, RepresentativePoint: bunching.Midpoint(e.PointA, e.PointB), DurationSeconds: e.LastObservedAt.Sub(e.FirstObservedAt).Seconds(), ConfirmationPending: e.MissingCaptures > 0 && e.ClosedAt == nil})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return page, err
	}
	return page, tx.Commit(ctx)
}
