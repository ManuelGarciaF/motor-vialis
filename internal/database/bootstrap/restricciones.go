package bootstrap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	scripts "github.com/ManuelGarciaF/vialis-motor/sql"
	"github.com/jackc/pgx/v5"
)

// streetRestriction is one OSM type=restriction relation, as it appears in the
// extract. Nothing is interpreted here: which relations apply to a bus, and to
// which edges, is decided by sql/calles/transformar_restricciones.sql.
type streetRestriction struct {
	RelationID int64
	Tags       map[string]string
	Members    []restrictionMember
}

// restrictionMember keeps the Spanish keys of the staging column it is stored
// in, so the SQL that reads it and the code that writes it use the same names.
type restrictionMember struct {
	Type string `json:"tipo"`
	Ref  int64  `json:"ref"`
	Role string `json:"rol"`
}

// importStreetRestrictions loads the turn restrictions of the streets file and
// maps them onto the road graph that transformar_calles.sql just published.
//
// osm2pgrouting cannot provide them: it only keeps relations whose tags appear
// in mapconfig.xml and, even then, stores their members without roles and
// without nodes, so the via node is lost. osmium is already required to read
// the extract date, and its OPL output is a line per relation.
func (e *executor) importStreetRestrictions(ctx context.Context) error {
	command := exec.CommandContext(ctx, "osmium", "tags-filter",
		"--omit-referenced",
		"--output-format", "opl,add_metadata=false",
		"--output", "-",
		e.streetsFile,
		"r/type=restriction",
	)
	command.Stderr = os.Stderr
	output, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("leer restricciones de giro con osmium: %w", err)
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("leer restricciones de giro con osmium: %w", err)
	}
	restrictions, parseErr := readRestrictionsOPL(output)
	if parseErr != nil {
		// Drain what is left so osmium does not block on a full pipe.
		_, _ = io.Copy(io.Discard, output)
	}
	if err := command.Wait(); err != nil {
		return fmt.Errorf("leer restricciones de giro con osmium: %w", err)
	}
	if parseErr != nil {
		return parseErr
	}

	if err := e.runScript(ctx, scripts.CrearRestriccionesRaw); err != nil {
		return err
	}
	copied, err := e.copyStreetRestrictions(ctx, restrictions)
	if err != nil {
		return err
	}
	e.logger.Info("restricciones de giro importadas", "relaciones", copied)
	return e.runScript(ctx, scripts.TransformarRestricciones)
}

// copyStreetRestrictions writes the relations into
// vialis.calles_restricciones_raw, which crear_restricciones_raw.sql creates.
func (e *executor) copyStreetRestrictions(
	ctx context.Context,
	restrictions []streetRestriction,
) (int64, error) {
	rows := make([][]any, 0, len(restrictions))
	for _, restriction := range restrictions {
		tags, err := json.Marshal(restriction.Tags)
		if err != nil {
			return 0, fmt.Errorf("codificar etiquetas de r%d: %w", restriction.RelationID, err)
		}
		members := restriction.Members
		if members == nil {
			members = []restrictionMember{}
		}
		encodedMembers, err := json.Marshal(members)
		if err != nil {
			return 0, fmt.Errorf("codificar miembros de r%d: %w", restriction.RelationID, err)
		}
		rows = append(rows, []any{restriction.RelationID, tags, encodedMembers})
	}
	copied, err := e.connection.CopyFrom(
		ctx,
		pgx.Identifier{SchemaName, "calles_restricciones_raw"},
		[]string{"osm_relation_id", "etiquetas", "miembros"},
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("copiar restricciones de giro: %w", err)
	}
	return copied, nil
}

// readRestrictionsOPL parses the relations of an OPL file, the format osmium
// writes one object per line in: "r<id> T<k>=<v>,... M<type><ref>@<role>,...".
// Other object types and the metadata fields are ignored.
func readRestrictionsOPL(reader io.Reader) ([]streetRestriction, error) {
	var restrictions []streetRestriction
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for number := 1; scanner.Scan(); number++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "r") {
			continue
		}
		restriction, err := parseRelationOPL(line)
		if err != nil {
			return nil, fmt.Errorf("línea OPL %d: %w", number, err)
		}
		restrictions = append(restrictions, restriction)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("leer OPL de restricciones: %w", err)
	}
	return restrictions, nil
}

func parseRelationOPL(line string) (streetRestriction, error) {
	fields := strings.Fields(line)
	id, err := strconv.ParseInt(fields[0][1:], 10, 64)
	if err != nil {
		return streetRestriction{}, fmt.Errorf("id de relación %q: %w", fields[0], err)
	}
	restriction := streetRestriction{RelationID: id, Tags: map[string]string{}}
	for _, field := range fields[1:] {
		switch field[0] {
		case 'T':
			if err := parseTagsOPL(field[1:], restriction.Tags); err != nil {
				return streetRestriction{}, fmt.Errorf("r%d: %w", id, err)
			}
		case 'M':
			members, err := parseMembersOPL(field[1:])
			if err != nil {
				return streetRestriction{}, fmt.Errorf("r%d: %w", id, err)
			}
			restriction.Members = members
		}
	}
	return restriction, nil
}

func parseTagsOPL(field string, tags map[string]string) error {
	if field == "" {
		return nil
	}
	for _, pair := range strings.Split(field, ",") {
		key, value, found := strings.Cut(pair, "=")
		if !found {
			return fmt.Errorf("etiqueta sin '=': %q", pair)
		}
		decodedKey, err := unescapeOPL(key)
		if err != nil {
			return err
		}
		decodedValue, err := unescapeOPL(value)
		if err != nil {
			return err
		}
		tags[decodedKey] = decodedValue
	}
	return nil
}

var memberTypes = map[byte]string{'n': "node", 'w': "way", 'r': "relation"}

func parseMembersOPL(field string) ([]restrictionMember, error) {
	if field == "" {
		return nil, nil
	}
	var members []restrictionMember
	for _, item := range strings.Split(field, ",") {
		reference, role, found := strings.Cut(item, "@")
		if !found || reference == "" {
			return nil, fmt.Errorf("miembro sin '@': %q", item)
		}
		memberType, known := memberTypes[reference[0]]
		if !known {
			return nil, fmt.Errorf("tipo de miembro desconocido: %q", item)
		}
		ref, err := strconv.ParseInt(reference[1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("referencia de miembro %q: %w", item, err)
		}
		decodedRole, err := unescapeOPL(role)
		if err != nil {
			return nil, err
		}
		members = append(members, restrictionMember{Type: memberType, Ref: ref, Role: decodedRole})
	}
	return members, nil
}

// unescapeOPL decodes the %<hex>% sequences OPL uses for the characters that
// would break its syntax (space, comma, equals, at sign, percent).
func unescapeOPL(value string) (string, error) {
	if !strings.Contains(value, "%") {
		return value, nil
	}
	var builder strings.Builder
	for {
		start := strings.IndexByte(value, '%')
		if start < 0 {
			builder.WriteString(value)
			return builder.String(), nil
		}
		builder.WriteString(value[:start])
		end := strings.IndexByte(value[start+1:], '%')
		if end < 0 {
			return "", errors.New("escape OPL sin cerrar: " + value)
		}
		code, err := strconv.ParseUint(value[start+1:start+1+end], 16, 32)
		if err != nil {
			return "", fmt.Errorf("escape OPL inválido en %q: %w", value, err)
		}
		builder.WriteRune(rune(code))
		value = value[start+end+2:]
	}
}
