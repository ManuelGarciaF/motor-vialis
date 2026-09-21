package bootstrap

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// columnKind is how a CSV field is turned into a value COPY can send. The CSV
// files carry text; the staging tables do not, and pgx encodes in binary, so
// the conversion has to be explicit. There are only three kinds because the
// staging tables only use three families of type: TEXT/CHAR(n), the integer
// types, and the floating-point ones.
type columnKind int

const (
	columnText columnKind = iota
	columnInteger
	columnDecimal
)

// column is one column of a staging table, in the order the CSV file lists it.
type column struct {
	Name string
	Kind columnKind
}

// csvFile maps one data file to the staging table it is copied into. The column
// order is the order of both the file header and the CREATE TABLE, and the
// importer refuses to load a file whose header says otherwise rather than
// silently shifting the values one column over.
type csvFile struct {
	FileName string
	Table    string
	Columns  []column
}

// columnNames returns the column list COPY is given.
func (f csvFile) columnNames() []string {
	names := make([]string, len(f.Columns))
	for index, definition := range f.Columns {
		names[index] = definition.Name
	}
	return names
}

func text(names ...string) []column {
	return columns(columnText, names...)
}

func integer(names ...string) []column {
	return columns(columnInteger, names...)
}

func decimal(names ...string) []column {
	return columns(columnDecimal, names...)
}

func columns(kind columnKind, names ...string) []column {
	result := make([]column, len(names))
	for index, name := range names {
		result[index] = column{Name: name, Kind: kind}
	}
	return result
}

func joinColumns(groups ...[]column) []column {
	var result []column
	for _, group := range groups {
		result = append(result, group...)
	}
	return result
}

// convert turns one CSV field into the value COPY sends. An empty field is
// NULL, which is what COPY ... FORMAT csv does with an unquoted empty field and
// what the transformation scripts expect: a trip without an expansion factor
// weighs zero, it does not weigh the empty string.
func convert(kind columnKind, raw string) (any, error) {
	if raw == "" {
		return nil, nil
	}
	switch kind {
	case columnText:
		return raw, nil
	case columnInteger:
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("valor entero inválido %q: %w", raw, err)
		}
		return value, nil
	case columnDecimal:
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("valor decimal inválido %q: %w", raw, err)
		}
		return value, nil
	default:
		return nil, fmt.Errorf("tipo de columna desconocido %d", kind)
	}
}

// checkHeader compares the header of a file against the columns of its staging
// table. Names are compared case-insensitively and without surrounding spaces,
// and a byte-order mark at the start of the file is ignored: some GTFS feeds
// ship one and it is not part of the first column's name.
func checkHeader(file csvFile, header []string) error {
	expected := file.columnNames()
	if len(header) != len(expected) {
		return fmt.Errorf(
			"%s tiene %d columnas y %s espera %d",
			file.FileName, len(header), file.Table, len(expected),
		)
	}
	for index, name := range header {
		clean := strings.TrimSpace(strings.TrimPrefix(name, "\ufeff"))
		if !strings.EqualFold(clean, expected[index]) {
			return fmt.Errorf(
				"%s: la columna %d se llama %q y %s espera %q",
				file.FileName, index+1, clean, file.Table, expected[index],
			)
		}
	}
	return nil
}

// csvSource feeds pgx.CopyFrom row by row, so a file of gigabytes never has to
// fit in memory. It also counts the rows it hands over, which is what lets the
// importer report progress on a load that takes minutes.
type csvSource struct {
	reader   *csv.Reader
	file     csvFile
	values   []any
	rows     int64
	err      error
	progress func(rows int64)
}

func newCSVSource(reader io.Reader, file csvFile, progress func(rows int64)) (*csvSource, error) {
	csvReader := csv.NewReader(reader)
	csvReader.ReuseRecord = true
	csvReader.LazyQuotes = true

	header, err := csvReader.Read()
	if err != nil {
		return nil, fmt.Errorf("leer el encabezado de %s: %w", file.FileName, err)
	}
	if err := checkHeader(file, header); err != nil {
		return nil, err
	}
	csvReader.FieldsPerRecord = len(file.Columns)

	return &csvSource{
		reader:   csvReader,
		file:     file,
		values:   make([]any, len(file.Columns)),
		progress: progress,
	}, nil
}

// Next reads the next record. It stops at the first unreadable one instead of
// skipping it: a row that does not parse means the file is not the file the
// staging table describes, and loading the rest would hide that.
func (s *csvSource) Next() bool {
	record, err := s.reader.Read()
	if err == io.EOF {
		return false
	}
	if err != nil {
		s.err = fmt.Errorf("leer %s en la fila %d: %w", s.file.FileName, s.rows+1, err)
		return false
	}
	for index, definition := range s.file.Columns {
		value, err := convert(definition.Kind, record[index])
		if err != nil {
			s.err = fmt.Errorf(
				"%s, fila %d, columna %s: %w",
				s.file.FileName, s.rows+1, definition.Name, err,
			)
			return false
		}
		s.values[index] = value
	}
	s.rows++
	if s.progress != nil {
		s.progress(s.rows)
	}
	return true
}

func (s *csvSource) Values() ([]any, error) {
	return s.values, nil
}

func (s *csvSource) Err() error {
	return s.err
}
