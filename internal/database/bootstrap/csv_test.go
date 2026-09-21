package bootstrap

import (
	"reflect"
	"strings"
	"testing"
)

func TestConvertProducesTypedValues(t *testing.T) {
	cases := []struct {
		name     string
		kind     columnKind
		raw      string
		expected any
		wantErr  bool
	}{
		{name: "texto", kind: columnText, raw: "7A", expected: "7A"},
		{name: "texto vacío es NULL", kind: columnText, raw: "", expected: nil},
		{name: "entero", kind: columnInteger, raw: "-12", expected: int64(-12)},
		{name: "entero vacío es NULL", kind: columnInteger, raw: "", expected: nil},
		{name: "entero inválido", kind: columnInteger, raw: "1.5", wantErr: true},
		{name: "decimal", kind: columnDecimal, raw: "-34.6", expected: -34.6},
		{name: "decimal vacío es NULL", kind: columnDecimal, raw: "", expected: nil},
		{name: "decimal inválido", kind: columnDecimal, raw: "x", wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			value, err := convert(testCase.kind, testCase.raw)
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("se esperaba un error para %q", testCase.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if !reflect.DeepEqual(value, testCase.expected) {
				t.Errorf("valor = %#v, se esperaba %#v", value, testCase.expected)
			}
		})
	}
}

func TestCheckHeaderRejectsAnUnexpectedFile(t *testing.T) {
	file := gtfsFiles[0]
	if err := checkHeader(file, file.columnNames()); err != nil {
		t.Fatalf("encabezado válido rechazado: %v", err)
	}
	// El BOM que traen algunos feeds no forma parte del nombre de la columna.
	withBOM := append([]string{}, file.columnNames()...)
	withBOM[0] = "\ufeff" + withBOM[0]
	if err := checkHeader(file, withBOM); err != nil {
		t.Errorf("encabezado con BOM rechazado: %v", err)
	}
	if err := checkHeader(file, file.columnNames()[:2]); err == nil {
		t.Error("se aceptó un encabezado con menos columnas")
	}
	swapped := append([]string{}, file.columnNames()...)
	swapped[0], swapped[1] = swapped[1], swapped[0]
	if err := checkHeader(file, swapped); err == nil {
		t.Error("se aceptó un encabezado con las columnas cambiadas de orden")
	}
}

func TestCSVSourceReadsEveryRecord(t *testing.T) {
	file := csvFile{
		FileName: "stops.txt",
		Table:    "vialis.gtfs_stops_raw",
		Columns:  gtfsFiles[3].Columns,
	}
	content := "stop_id,stop_code,stop_name,stop_lat,stop_lon\r\n" +
		"1,A,Retiro,-34.59,-58.37\r\n" +
		"2,,Once,,-58.40\r\n"

	source, err := newCSVSource(strings.NewReader(content), file, nil)
	if err != nil {
		t.Fatalf("crear la fuente: %v", err)
	}

	var rows [][]any
	for source.Next() {
		values, err := source.Values()
		if err != nil {
			t.Fatalf("leer valores: %v", err)
		}
		rows = append(rows, append([]any{}, values...))
	}
	if err := source.Err(); err != nil {
		t.Fatalf("error de lectura: %v", err)
	}

	expected := [][]any{
		{"1", "A", "Retiro", -34.59, -58.37},
		{"2", nil, "Once", nil, -58.40},
	}
	if !reflect.DeepEqual(rows, expected) {
		t.Fatalf("filas = %#v, se esperaba %#v", rows, expected)
	}
}

func TestCSVSourceStopsAtAnUnparseableValue(t *testing.T) {
	file := csvFile{
		FileName: "calendar_dates.txt",
		Table:    "vialis.gtfs_calendar_dates_raw",
		Columns:  gtfsFiles[6].Columns,
	}
	content := "service_id,date,exception_type\n1,20241016,1\n2,20241017,mañana\n"

	source, err := newCSVSource(strings.NewReader(content), file, nil)
	if err != nil {
		t.Fatalf("crear la fuente: %v", err)
	}
	count := 0
	for source.Next() {
		count++
	}
	if count != 1 {
		t.Errorf("filas leídas = %d, se esperaba 1", count)
	}
	if source.Err() == nil {
		t.Fatal("se esperaba un error por el valor inválido")
	}
	if !strings.Contains(source.Err().Error(), "exception_type") {
		t.Errorf("el error no nombra la columna: %v", source.Err())
	}
}

func TestNewCSVSourceRejectsAFileWithAnotherHeader(t *testing.T) {
	_, err := newCSVSource(strings.NewReader("a,b,c\n"), gtfsFiles[0], nil)
	if err == nil {
		t.Fatal("se aceptó un archivo con otro encabezado")
	}
	if !strings.Contains(err.Error(), gtfsFiles[0].FileName) {
		t.Errorf("el error no nombra el archivo: %v", err)
	}
}

func TestTableIdentifierKeepsSchemaAndTableSeparate(t *testing.T) {
	identifier := tableIdentifier("vialis.gtfs_agency_raw")
	if !reflect.DeepEqual([]string(identifier), []string{"vialis", "gtfs_agency_raw"}) {
		t.Fatalf("identificador = %#v", identifier)
	}
}
