package bootstrap

import (
	"reflect"
	"strings"
	"testing"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
)

// Las líneas salen de osmium tags-filter sobre el extracto AMBA: una restricción
// de nodo, una condicional con escapes y un miembro sin rol, que OSM tiene.
func TestReadRestrictionsOPL(t *testing.T) {
	input := strings.Join([]string{
		"r1435772 Trestriction=no_left_turn,type=restriction Mw1452751314@from,w10434820@to,n89318040@via",
		"r2 Trestriction:conditional=no_right_turn%20%%40%%20%(Mo-Fr%20%08:00-20:00),type=restriction Mw1@from,n2@,w3@to",
		"n5 v1 Tfoo=bar x-58.4 y-34.5",
		"r3 T M",
		"",
	}, "\n")

	got, err := readRestrictionsOPL(strings.NewReader(input))
	if err != nil {
		t.Fatalf("readRestrictionsOPL: %v", err)
	}
	want := []streetRestriction{
		{
			RelationID: 1435772,
			Tags:       map[string]string{"restriction": "no_left_turn", "type": "restriction"},
			Members: []restrictionMember{
				{Type: "way", Ref: 1452751314, Role: "from"},
				{Type: "way", Ref: 10434820, Role: "to"},
				{Type: "node", Ref: 89318040, Role: "via"},
			},
		},
		{
			RelationID: 2,
			Tags: map[string]string{
				"restriction:conditional": "no_right_turn @ (Mo-Fr 08:00-20:00)",
				"type":                    "restriction",
			},
			Members: []restrictionMember{
				{Type: "way", Ref: 1, Role: "from"},
				{Type: "node", Ref: 2, Role: ""},
				{Type: "way", Ref: 3, Role: "to"},
			},
		},
		{RelationID: 3, Tags: map[string]string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("restricciones = %#v\nse esperaba %#v", got, want)
	}
}

func TestReadRestrictionsOPLRejectsMalformedLines(t *testing.T) {
	for _, line := range []string{
		"rX Ttype=restriction",
		"r1 Ttype",
		"r1 Mw1",
		"r1 Mx1@from",
		"r1 Tname=a%20b",
	} {
		if _, err := readRestrictionsOPL(strings.NewReader(line)); err == nil {
			t.Errorf("%q no devolvió error", line)
		}
	}
}

// transformar_calles.sql vacía calles y calles_vertices, y calles_restricciones
// las referencia: si no viaja en el mismo TRUNCATE, Postgres lo rechaza con
// SQLSTATE 0A000 y la carga de calles muere.
func TestLaTransformacionDeCallesVaciaLasRestriccionesQueLaReferencian(t *testing.T) {
	upper := strings.ToUpper(scripts.TransformarCalles)
	truncate := strings.Index(upper, "TRUNCATE TABLE")
	if truncate < 0 {
		t.Fatal("transformar_calles.sql ya no hace TRUNCATE: revisar esta invariante")
	}
	end := strings.Index(upper[truncate:], ";")
	if end < 0 {
		t.Fatal("el TRUNCATE de transformar_calles.sql no termina en punto y coma")
	}
	if !strings.Contains(scripts.TransformarCalles[truncate:truncate+end], "vialis.calles_restricciones") {
		t.Error("el TRUNCATE de transformar_calles.sql no incluye vialis.calles_restricciones")
	}
}
