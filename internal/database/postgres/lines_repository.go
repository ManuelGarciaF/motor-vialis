package postgres

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ManuelGarciaF/vialis-motor/internal/lines"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed list_lines.sql
var listLinesSQL string

//go:embed find_line.sql
var findLineSQL string

//go:embed find_similar_lines.sql
var findSimilarLinesSQL string

// LinesRepository reads the stored GTFS lines.
type LinesRepository struct {
	query queryFunc
}

// NewLinesRepository creates a PostgreSQL stored-line repository.
func NewLinesRepository(database *pgxpool.Pool) *LinesRepository {
	return newLinesRepository(func(
		ctx context.Context,
		sql string,
		arguments ...any,
	) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newLinesRepository(query queryFunc) *LinesRepository {
	return &LinesRepository{query: query}
}

// ListSummaries returns one page of stored lines and how many matched in total.
func (repository *LinesRepository) ListSummaries(
	ctx context.Context,
	query lines.Query,
) ([]lines.Summary, int, error) {
	var (
		minLongitude *float64
		minLatitude  *float64
		maxLongitude *float64
		maxLatitude  *float64
	)
	if query.Bounds != nil {
		minLongitude = &query.Bounds.MinLongitude
		minLatitude = &query.Bounds.MinLatitude
		maxLongitude = &query.Bounds.MaxLongitude
		maxLatitude = &query.Bounds.MaxLatitude
	}

	rows, err := repository.query(
		ctx,
		listLinesSQL,
		escapeLikePattern(query.Search),
		minLongitude,
		minLatitude,
		maxLongitude,
		maxLatitude,
		query.Limit,
		query.Offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("query stored lines: %w", err)
	}
	defer rows.Close()

	var (
		summaries []lines.Summary
		total     int
	)
	for rows.Next() {
		var (
			summary  lines.Summary
			rowTotal int
		)
		if err := rows.Scan(
			&summary.ID,
			&summary.Line,
			&summary.Branch,
			&summary.PublicName,
			&summary.DirectionID,
			&summary.Destination,
			&summary.Description,
			&summary.DistanceMeters,
			&summary.StopCount,
			&rowTotal,
		); err != nil {
			return nil, 0, fmt.Errorf("scan stored line: %w", err)
		}
		summaries = append(summaries, summary)
		total = rowTotal
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate stored lines: %w", err)
	}
	return summaries, total, nil
}

// FindLine returns one stored line with its stops in route order, or
// lines.ErrNotFound.
func (repository *LinesRepository) FindLine(
	ctx context.Context,
	id int64,
) (lines.StoredLine, error) {
	rows, err := repository.query(ctx, findLineSQL, id)
	if err != nil {
		return lines.StoredLine{}, fmt.Errorf("query stored line: %w", err)
	}
	defer rows.Close()

	var (
		stored lines.StoredLine
		found  bool
	)
	for rows.Next() {
		var (
			summary    lines.Summary
			stopNumber sql.NullInt32
			gtfsStopID sql.NullString
			name       sql.NullString
			code       sql.NullString
			latitude   sql.NullFloat64
			longitude  sql.NullFloat64
			path       sql.NullString
		)
		if err := rows.Scan(
			&summary.ID,
			&summary.Line,
			&summary.Branch,
			&summary.PublicName,
			&summary.DirectionID,
			&summary.Destination,
			&summary.Description,
			&summary.DistanceMeters,
			&stopNumber,
			&gtfsStopID,
			&name,
			&code,
			&latitude,
			&longitude,
			&path,
		); err != nil {
			return lines.StoredLine{}, fmt.Errorf("scan stored line stop: %w", err)
		}

		if !found {
			stored.Summary = summary
			found = true
		}
		// The LEFT JOIN keeps a stopless line visible; that row carries no stop.
		if !stopNumber.Valid || !gtfsStopID.Valid ||
			!latitude.Valid || !longitude.Valid {
			continue
		}

		stop := lines.StoredStop{
			StopNumber: int(stopNumber.Int32),
			GTFSStopID: gtfsStopID.String,
			Name:       name.String,
			Code:       code.String,
			Position: route.Position{
				Latitude:  latitude.Float64,
				Longitude: longitude.Float64,
			},
		}
		if path.Valid {
			var lineString route.LineString
			if err := json.Unmarshal([]byte(path.String), &lineString); err != nil {
				return lines.StoredLine{}, fmt.Errorf(
					"decode stored path after stop %d: %w",
					stop.StopNumber,
					err,
				)
			}
			stop.PathToNext = &lineString
		}
		stored.Stops = append(stored.Stops, stop)
	}
	if err := rows.Err(); err != nil {
		return lines.StoredLine{}, fmt.Errorf("iterate stored line stops: %w", err)
	}
	if !found {
		return lines.StoredLine{}, lines.ErrNotFound
	}

	stored.Summary.StopCount = len(stored.Stops)
	return stored, nil
}

// FindSimilar returns the stored lines sharing a corridor with the drawn path,
// ranked and cut to the query's limit by the database.
//
// The ranking and the limit stay in SQL rather than being applied to whatever
// the database happened to return: the limit only means anything once the rows
// are ordered, and ordering a truncated set is not the same list.
func (repository *LinesRepository) FindSimilar(
	ctx context.Context,
	query lines.SimilarityQuery,
) ([]lines.Similarity, error) {
	encodedPath, err := json.Marshal(query.Path)
	if err != nil {
		return nil, fmt.Errorf("encode drawn path: %w", err)
	}

	rows, err := repository.query(
		ctx,
		findSimilarLinesSQL,
		string(encodedPath),
		query.Origin.Longitude,
		query.Origin.Latitude,
		query.Destination.Longitude,
		query.Destination.Latitude,
		query.CorridorToleranceMeters,
		query.MinimumCoverage,
		query.Limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query similar stored lines: %w", err)
	}
	defer rows.Close()

	var similarities []lines.Similarity
	for rows.Next() {
		var (
			similarity   lines.Similarity
			totalMinutes sql.NullInt64
			flow         sql.NullInt64
			revenue      sql.NullInt64
		)
		if err := rows.Scan(
			&similarity.Line.ID,
			&similarity.Line.Line,
			&similarity.Line.Branch,
			&similarity.Line.PublicName,
			&similarity.Line.DirectionID,
			&similarity.Line.Destination,
			&similarity.Line.Description,
			&similarity.Line.DistanceMeters,
			&similarity.Line.StopCount,
			&similarity.CoverageOfProposed,
			&similarity.CoverageOfStored,
			&similarity.OriginDistanceMeters,
			&similarity.DestinationDistanceMeters,
			&totalMinutes,
			&flow,
			&revenue,
		); err != nil {
			return nil, fmt.Errorf("scan similar stored line: %w", err)
		}

		// A NULL metric stays absent all the way up. The columns are nullable
		// because the pipeline does not have a ridership or a revenue figure
		// for every line, and turning "nobody measured this" into 0 would let
		// it be read as "this line carries nobody".
		similarity.Metrics = lines.StoredMetrics{
			DistanceMeters: similarity.Line.DistanceMeters,
		}
		if totalMinutes.Valid {
			minutes := int(totalMinutes.Int64)
			similarity.Metrics.TotalMinutes = &minutes
		}
		if flow.Valid {
			passengers := int(flow.Int64)
			similarity.Metrics.PassengerFlow = &passengers
		}
		if revenue.Valid {
			amount := revenue.Int64
			similarity.Metrics.Revenue = &amount
		}
		similarities = append(similarities, similarity)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate similar stored lines: %w", err)
	}
	return similarities, nil
}

// escapeLikePattern neutralises the wildcards ILIKE would otherwise read in
// user input, so searching for "132_A" looks for that text and not for any
// character in the underscore's place.
func escapeLikePattern(search string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return replacer.Replace(search)
}

var _ lines.Repository = (*LinesRepository)(nil)
