package dbconn

import "strings"

type StatementKind string

const (
	StatementRead    StatementKind = "read"
	StatementWrite   StatementKind = "write"
	StatementUnknown StatementKind = "unknown"
)

// ClassifySQL is a conservative routing filter, not the security boundary.
// Reads also execute in a database-enforced read-only transaction. Ambiguous
// quoting, executable comments and statement batches are rejected outright.
func ClassifySQL(text string) StatementKind {
	tokens, ok := sqlTokens(text)
	if !ok || len(tokens) == 0 {
		return StatementUnknown
	}
	switch tokens[0] {
	case "SELECT", "WITH", "SHOW", "DESCRIBE", "DESC", "EXPLAIN":
		for _, token := range tokens {
			switch token {
			case "INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE", "UPSERT",
				"CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COPY",
				"VACUUM", "ANALYZE", "ANALYSE", "REINDEX", "ATTACH", "DETACH",
				"CALL", "DO", "EXECUTE", "EXEC", "SET", "INTO", "COMMIT", "ROLLBACK":
				return StatementWrite
			}
		}
		return StatementRead
	case "PRAGMA":
		if len(tokens) < 2 {
			return StatementUnknown
		}
		for _, token := range tokens {
			if token == "=" {
				return StatementWrite
			}
		}
		switch tokens[1] {
		case "TABLE_INFO", "TABLE_XINFO", "INDEX_LIST", "INDEX_INFO", "INDEX_XINFO",
			"FOREIGN_KEY_LIST", "DATABASE_LIST", "COMPILE_OPTIONS", "COLLATION_LIST",
			"INTEGRITY_CHECK", "QUICK_CHECK":
			return StatementRead
		}
		return StatementWrite
	case "INSERT", "UPDATE", "DELETE", "REPLACE", "MERGE", "UPSERT",
		"CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT", "REVOKE", "COPY",
		"VACUUM", "ANALYZE", "REINDEX", "ATTACH", "DETACH", "CALL", "DO",
		"EXECUTE", "EXEC", "SET", "BEGIN", "COMMIT", "ROLLBACK":
		return StatementWrite
	}
	return StatementUnknown
}

func sqlTokens(text string) ([]string, bool) {
	var tokens []string
	ended := false
	for i := 0; i < len(text); {
		ch := text[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f' {
			i++
			continue
		}
		if ch == '-' && i+1 < len(text) && text[i+1] == '-' {
			// MySQL requires whitespace after --; reject dialect-ambiguous comments.
			if i+2 < len(text) && text[i+2] != ' ' && text[i+2] != '\t' && text[i+2] != '\r' && text[i+2] != '\n' {
				return nil, false
			}
			for i < len(text) && text[i] != '\n' && text[i] != '\r' {
				i++
			}
			continue
		}
		if ch == '/' && i+1 < len(text) && text[i+1] == '*' {
			if i+2 < len(text) && (text[i+2] == '!' || text[i+2] == '+') {
				return nil, false
			}
			i += 2
			closed := false
			for i+1 < len(text) {
				// Nested comments have different semantics across supported drivers.
				if text[i] == '/' && text[i+1] == '*' {
					return nil, false
				}
				if text[i] == '*' && text[i+1] == '/' {
					i += 2
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			continue
		}
		if ended {
			return nil, false
		}
		if ch == ';' {
			ended = true
			i++
			continue
		}
		if ch == '\'' || ch == '"' || ch == 96 {
			quote := ch
			i++
			closed := false
			for i < len(text) {
				if text[i] == '\\' {
					return nil, false
				} // depends on server SQL mode
				if text[i] == quote {
					if i+1 < len(text) && text[i+1] == quote {
						i += 2
						continue
					}
					i++
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, false
			}
			tokens = append(tokens, "<quoted>")
			continue
		}
		if ch == '$' || ch == '#' || ch == '\\' {
			return nil, false
		}
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch == '_' {
			start := i
			i++
			for i < len(text) {
				c := text[i]
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
					break
				}
				i++
			}
			tokens = append(tokens, strings.ToUpper(text[start:i]))
			continue
		}
		tokens = append(tokens, string(ch))
		i++
	}
	return tokens, true
}
