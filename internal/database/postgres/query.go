package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type rowIterator interface {
	Next() bool
	Scan(destinations ...any) error
	Err() error
	Close()
}

type queryFunc func(
	ctx context.Context,
	sql string,
	arguments ...any,
) (rowIterator, error)

var _ rowIterator = pgx.Rows(nil)
