package postgres

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/revenue"
	"github.com/ManuelGarciaF/vialis-motor/internal/simulation/route"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed find_tariff_bands.sql
var findTariffBandsSQL string

// RevenueRepository reads fare bands from PostgreSQL.
type RevenueRepository struct{ query queryFunc }

func NewRevenueRepository(database *pgxpool.Pool) *RevenueRepository {
	return newRevenueRepository(func(ctx context.Context, sql string, arguments ...any) (rowIterator, error) {
		return database.Query(ctx, sql, arguments...)
	})
}

func newRevenueRepository(query queryFunc) *RevenueRepository {
	return &RevenueRepository{query: query}
}

func (repository *RevenueRepository) FindTariffBands(ctx context.Context, jurisdiction route.Jurisdiction) ([]revenue.TariffBand, error) {
	rows, err := repository.query(ctx, findTariffBandsSQL, string(jurisdiction))
	if err != nil {
		return nil, fmt.Errorf("query tariff bands: %w", err)
	}
	defer rows.Close()
	bands := make([]revenue.TariffBand, 0)
	for rows.Next() {
		var band revenue.TariffBand
		if err := rows.Scan(&band.MinimumDistanceMeters, &band.MaximumDistanceMeters, &band.RegisteredFareCents, &band.UnregisteredFareCents); err != nil {
			return nil, fmt.Errorf("scan tariff band: %w", err)
		}
		bands = append(bands, band)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tariff bands: %w", err)
	}
	return bands, nil
}

var _ revenue.Repository = (*RevenueRepository)(nil)
