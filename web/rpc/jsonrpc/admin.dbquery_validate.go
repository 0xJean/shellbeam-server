package jsonrpc

import (
	"errors"
	"strings"

	"github.com/komari-monitor/komari/pkg/metric"
)

var errNotReadOnlySQL = errors.New("dbQuery only runs a single read statement; use dbExec for anything else")

// readOnlySQLiteMetadataPragmas are the PRAGMAs dbQuery runs. Others can change
// connection state, including query_only itself.
var readOnlySQLiteMetadataPragmas = map[string]bool{
	"table_info": true, "table_xinfo": true, "table_list": true,
	"index_list": true, "index_info": true, "index_xinfo": true, "foreign_key_list": true,
}

// validateReadOnlySQL admits one statement that starts with a read keyword. This
// covers what a read-only connection does not: VACUUM INTO and ATTACH write other
// files, SELECT INTO OUTFILE writes on the MySQL host, and a second statement could
// lift the read-only state. The database still refuses writes such as WITH ... DELETE.
func validateReadOnlySQL(driver metric.Driver, statement string) error {
	words, hasAssignment, err := scanSQLWords(driver, statement)
	if err != nil {
		return err
	}
	if len(words) == 0 {
		return errNotReadOnlySQL
	}
	for _, word := range words {
		if word == "into" {
			return errNotReadOnlySQL
		}
	}
	switch words[0] {
	case "select", "with", "values", "explain":
		return nil
	case "show", "describe", "desc":
		if driver != metric.DriverSQLite {
			return nil
		}
	case "pragma":
		if driver == metric.DriverSQLite && len(words) > 1 && readOnlySQLiteMetadataPragmas[words[1]] && !hasAssignment {
			return nil
		}
	}
	return errNotReadOnlySQL
}

// scanSQLWords returns the lowercase bare words outside strings, quoted names and
// comments. It fails on a statement separator followed by anything else.
func scanSQLWords(driver metric.Driver, statement string) (words []string, hasAssignment bool, err error) {
	mysql := driver == metric.DriverMySQL
	ended := false
	for i := 0; i < len(statement); {
		ch := statement[i]
		next := byte(0)
		if i+1 < len(statement) {
			next = statement[i+1]
		}
		switch {
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f':
			i++
			continue
		case ch == '-' && next == '-' && (!mysql || i+2 >= len(statement) || strings.ContainsRune(" \t\n\r", rune(statement[i+2]))),
			ch == '#' && mysql:
			end := strings.IndexByte(statement[i:], '\n')
			if end < 0 {
				return words, hasAssignment, nil
			}
			i += end + 1
			continue
		case ch == '/' && next == '*':
			// MySQL executes the body of /*! ... */.
			if mysql && i+2 < len(statement) && statement[i+2] == '!' {
				return nil, false, errNotReadOnlySQL
			}
			end := strings.Index(statement[i+2:], "*/")
			if end < 0 {
				return words, hasAssignment, nil
			}
			i += end + 4
			continue
		}
		if ended {
			return nil, false, errNotReadOnlySQL
		}
		switch {
		case ch == ';':
			ended = true
			i++
		case ch == '\'' || ch == '"' || ch == '`' || ch == '[':
			closer := ch
			if ch == '[' {
				if driver != metric.DriverSQLite {
					i++
					continue
				}
				closer = ']'
			}
			i++
			for i < len(statement) {
				if mysql && statement[i] == '\\' && closer != '`' {
					i += 2
					continue
				}
				if statement[i] != closer {
					i++
					continue
				}
				if i+1 < len(statement) && statement[i+1] == closer && closer != ']' {
					i += 2
					continue
				}
				break
			}
			if i >= len(statement) {
				return nil, false, errNotReadOnlySQL
			}
			i++
		case isSQLWordByte(ch):
			start := i
			for i < len(statement) && isSQLWordByte(statement[i]) {
				i++
			}
			words = append(words, strings.ToLower(statement[start:i]))
		default:
			if ch == '=' {
				hasAssignment = true
			}
			i++
		}
	}
	return words, hasAssignment, nil
}

func isSQLWordByte(ch byte) bool {
	return ch == '_' || ch == '$' || ch >= 0x80 ||
		(ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')
}
