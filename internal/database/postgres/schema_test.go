package postgres

import "testing"

func TestQueryInSchema(t *testing.T) {
	query := "SELECT * FROM vialis.recorridos r JOIN vialis.paradas p ON p.id_parada=$1 WHERE r.linea=$2"
	for _, tc := range []struct{ schema, want string }{
		{"vialis", `SELECT * FROM "vialis".recorridos r JOIN "vialis".paradas p ON p.id_parada=$1 WHERE r.linea=$2`},
		{"fixture", `SELECT * FROM "fixture".recorridos r JOIN "fixture".paradas p ON p.id_parada=$1 WHERE r.linea=$2`},
		{`fixture"quoted`, `SELECT * FROM "fixture""quoted".recorridos r JOIN "fixture""quoted".paradas p ON p.id_parada=$1 WHERE r.linea=$2`},
	} {
		if got := queryInSchema(query, tc.schema); got != tc.want {
			t.Fatalf("schema=%q: %s", tc.schema, got)
		}
	}

	if got := queryInSchema("SELECT $1 FROM realtime_positions_batch", "fixture"); got != "SELECT $1 FROM realtime_positions_batch" {
		t.Fatal(got)
	}
}
