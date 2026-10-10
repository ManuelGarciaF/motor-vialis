package postgres

import (
	"strings"

	"github.com/jackc/pgx/v5"
)

// queryInSchema adapts repository SQL to an isolated schema. Only the schema
// identifier is interpolated and quoted; query values remain pgx parameters.
func queryInSchema(query, schema string) string {
	return strings.ReplaceAll(query, "vialis.", pgx.Identifier{schema}.Sanitize()+".")
}
