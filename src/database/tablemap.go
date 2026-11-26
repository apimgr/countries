package database

import (
	"strings"
)

var serverDatabaseIdentifiers = map[string]struct{}{
	"api_tokens": {}, "app_secrets": {}, "audit_log": {}, "backups": {},
	"captcha_challenges": {}, "config": {}, "config_meta": {}, "health_request_hours": {},
	"health_request_stats": {}, "owners": {}, "pgp_keys": {}, "rate_limits": {},
	"scheduler_history": {}, "scheduler_tasks": {}, "security_reports": {}, "security_thanks": {},
	"idx_api_tokens_active": {}, "idx_api_tokens_hash": {}, "idx_api_tokens_owner": {},
	"idx_api_tokens_prefix": {}, "idx_api_tokens_primary": {}, "idx_api_tokens_token_hash": {},
	"idx_audit_actor_ip": {}, "idx_audit_category": {}, "idx_audit_target": {},
	"idx_audit_timestamp": {}, "idx_backups_created": {}, "idx_captcha_challenges_expires": {},
	"idx_config_key_prefix": {}, "idx_health_request_hours": {}, "idx_owners_last_seen": {},
	"idx_pgp_keys_expiry": {}, "idx_rate_limits_window": {}, "idx_scheduler_history_started": {},
	"idx_scheduler_history_task": {}, "idx_scheduler_tasks_enabled": {},
	"idx_security_reports_status": {}, "idx_security_thanks_disclosed": {},
}

// DatabaseIdentifier is the sole owner of table and index names across local
// SQLite and remote libSQL databases.
func DatabaseIdentifier(driverName, identifier string) string {
	if normalizeDatabaseDriver(driverName) != "libsql" || strings.HasPrefix(identifier, "srv_") {
		return identifier
	}
	if _, ok := serverDatabaseIdentifiers[strings.ToLower(identifier)]; !ok {
		return identifier
	}
	return "srv_" + identifier
}

type sqlToken struct {
	start int
	end   int
	kind  byte
}

// rewriteRemoteSQL maps known table and index identifiers without changing
// literals, comments, column names, aliases, or other SQL text.
func rewriteRemoteSQL(query string) string {
	tokens := tokenizeSQL(query)
	if len(tokens) == 0 {
		return query
	}
	var replacements []sqlReplacement
	expectTable := false
	expectIndex := false
	createIndex := false
	createTrigger := false
	for index := range tokens {
		token := tokens[index]
		value := query[token.start:token.end]
		if token.kind == 's' && expectTable {
			if len(value) >= 2 {
				quote := value[0]
				name := value[1 : len(value)-1]
				mapped := DatabaseIdentifier("libsql", name)
				if mapped != name {
					replacements = append(replacements, sqlReplacement{token.start, token.end, string(quote) + mapped + string(quote)})
				}
			}
			expectTable = false
			continue
		}
		if token.kind != 'w' && token.kind != 'q' {
			if token.kind == ';' {
				createIndex = false
				createTrigger = false
			}
			continue
		}
		lower := strings.ToLower(unquoteSQLIdentifier(value))
		if expectTable || expectIndex {
			if lower == "pragma_table_info" {
				expectTable = true
				continue
			}
			if isSQLKeyword(lower) {
				continue
			}
			mapped := DatabaseIdentifier("libsql", unquoteSQLIdentifier(value))
			if mapped != unquoteSQLIdentifier(value) {
				replacements = append(replacements, sqlReplacement{token.start, token.end, quoteSQLIdentifier(value, mapped)})
			}
			expectTable = false
			if expectIndex {
				createIndex = true
			}
			expectIndex = false
			continue
		}
		switch lower {
		case "from", "join", "into", "update", "references", "table":
			expectTable = true
		case "on":
			if createIndex || createTrigger {
				expectTable = true
			}
		case "index":
			expectIndex = true
		case "trigger":
			createTrigger = true
		case "table_info", "pragma_table_info":
			expectTable = true
		}
	}
	if len(replacements) == 0 {
		return query
	}
	var result strings.Builder
	result.Grow(len(query) + len(replacements)*4)
	position := 0
	for _, replacement := range replacements {
		result.WriteString(query[position:replacement.start])
		result.WriteString(replacement.value)
		position = replacement.end
	}
	result.WriteString(query[position:])
	return result.String()
}

type sqlReplacement struct {
	start int
	end   int
	value string
}

func tokenizeSQL(query string) []sqlToken {
	tokens := make([]sqlToken, 0, len(query)/5)
	for index := 0; index < len(query); {
		start := index
		current := query[index]
		if isSQLSpace(current) {
			index++
			continue
		}
		if current == '-' && index+1 < len(query) && query[index+1] == '-' {
			index += 2
			for index < len(query) && query[index] != '\n' {
				index++
			}
			continue
		}
		if current == '/' && index+1 < len(query) && query[index+1] == '*' {
			index += 2
			for index+1 < len(query) && (query[index] != '*' || query[index+1] != '/') {
				index++
			}
			if index+1 < len(query) {
				index += 2
			}
			continue
		}
		if current == '\'' {
			index++
			for index < len(query) {
				if query[index] == '\'' {
					index++
					if index < len(query) && query[index] == '\'' {
						index++
						continue
					}
					break
				}
				index++
			}
			tokens = append(tokens, sqlToken{start, index, 's'})
			continue
		}
		if current == '"' || current == '`' || current == '[' {
			closing := current
			if current == '[' {
				closing = ']'
			}
			index++
			for index < len(query) {
				if query[index] == closing {
					index++
					break
				}
				index++
			}
			tokens = append(tokens, sqlToken{start, index, 'q'})
			continue
		}
		if isSQLIdentifierStart(current) {
			index++
			for index < len(query) && isSQLIdentifierPart(query[index]) {
				index++
			}
			tokens = append(tokens, sqlToken{start, index, 'w'})
			continue
		}
		index++
		tokens = append(tokens, sqlToken{start, index, current})
	}
	return tokens
}

func isSQLIdentifierStart(value byte) bool {
	return value == '_' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}

func isSQLIdentifierPart(value byte) bool {
	return isSQLIdentifierStart(value) || value >= '0' && value <= '9'
}

func isSQLSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f'
}

func isSQLKeyword(value string) bool {
	switch value {
	case "if", "not", "exists", "or", "ignore", "replace", "abort", "fail", "rollback", "only", "select":
		return true
	default:
		return false
	}
}

func unquoteSQLIdentifier(value string) string {
	if len(value) < 2 {
		return value
	}
	if value[0] == '[' && value[len(value)-1] == ']' {
		return value[1 : len(value)-1]
	}
	if value[0] == value[len(value)-1] && (value[0] == '`' || value[0] == '"') {
		return strings.ReplaceAll(value[1:len(value)-1], string(value[0])+string(value[0]), string(value[0]))
	}
	return value
}

func quoteSQLIdentifier(original, mapped string) string {
	if len(original) < 2 {
		return mapped
	}
	opening, closing := original[0], original[len(original)-1]
	if opening == '[' && closing == ']' {
		return "[" + strings.ReplaceAll(mapped, "]", "]]") + "]"
	}
	if opening == closing && (opening == '`' || opening == '"') {
		quote := string(opening)
		return quote + strings.ReplaceAll(mapped, quote, quote+quote) + quote
	}
	return mapped
}
