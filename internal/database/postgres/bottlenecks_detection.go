package postgres

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/ManuelGarciaF/vialis-motor/internal/bottlenecks"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_detected_bottlenecks.sql
var findDetectedBottlenecksSQL string

type detectionQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}
type BottlenecksDetectionRepository struct {
	database detectionQuerier
	schema   string
}

func NewBottlenecksDetectionRepository(pool *pgxpool.Pool) *BottlenecksDetectionRepository {
	return &BottlenecksDetectionRepository{database: pool, schema: "vialis"}
}

var _ bottlenecks.DetectionRepository = (*BottlenecksDetectionRepository)(nil)

func (r *BottlenecksDetectionRepository) FindDetectedSectors(ctx context.Context, model string, q bottlenecks.DetectionQuery, p bottlenecks.DetectionPolicy) (bottlenecks.DetectionData, error) {
	bounds := bottlenecks.DetectionBounds{}
	if q.Bounds != nil {
		bounds = *q.Bounds
	}
	var raw []byte
	err := r.database.QueryRow(ctx, queryInSchema(findDetectedBottlenecksSQL, r.schema),
		model, q.From, q.To, p.SpeedBelowKPH, p.MinimumSlowPassages, q.Line, q.Bounds != nil,
		bounds.MinLongitude, bounds.MinLatitude, bounds.MaxLongitude, bounds.MaxLatitude).Scan(&raw)
	if err != nil {
		return bottlenecks.DetectionData{}, err
	}
	var data bottlenecks.DetectionData
	err = json.Unmarshal(raw, &data)
	return data, err
}
