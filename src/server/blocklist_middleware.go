package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"strings"
)

const blocklistRequestBodyLimit = 2 << 20

func (s *Server) blocklistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.securityData == nil || isAllowlisted(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		if addr, err := netip.ParseAddr(requestClientIP(r.RemoteAddr)); err == nil {
			addr = addr.Unmap()
			if id, blocked := s.securityData.MatchIP(addr); blocked {
				log.Printf("security.ip_blocked ip=%q reason=%q", addr, id)
				writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "FORBIDDEN", "message": "Request is blocked"})
				return
			}
		}
		targets, tooLarge, err := blocklistTargets(r)
		if tooLarge {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"ok": false, "error": "REQUEST_TOO_LARGE", "message": "Request body is too large"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": "INVALID_REQUEST", "message": "Request body could not be read"})
			return
		}
		for _, target := range targets {
			if id, blocked := s.securityData.MatchDomain(target); blocked {
				log.Printf("security.domain_blocked source=%q", id)
				writeJSON(w, http.StatusForbidden, map[string]any{"ok": false, "error": "FORBIDDEN", "message": "Request is blocked"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func blocklistTargets(r *http.Request) ([]string, bool, error) {
	targets := make([]string, 0, 4)
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		targets = append(targets, origin)
	} else if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		targets = append(targets, referer)
	}
	for key, values := range r.URL.Query() {
		if isURLField(key) {
			targets = append(targets, values...)
		}
	}
	if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
		return targets, false, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, blocklistRequestBodyLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(body) > blocklistRequestBodyLimit {
		return nil, true, nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	switch contentType {
	case "application/x-www-form-urlencoded":
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, false, err
		}
		for key, values := range form {
			if isURLField(key) {
				targets = append(targets, values...)
				continue
			}
			if strings.EqualFold(key, "email") {
				for _, value := range values {
					if address, parseErr := mail.ParseAddress(value); parseErr == nil {
						targets = append(targets, address.Address)
					}
				}
			}
		}
	case "application/json":
		var values map[string]json.RawMessage
		if err := json.Unmarshal(body, &values); err != nil {
			return nil, false, err
		}
		collectJSONTargets(values, &targets)
	}
	return targets, false, nil
}

func collectJSONTargets(values map[string]json.RawMessage, targets *[]string) {
	for key, raw := range values {
		if isURLField(key) {
			collectJSONStrings(raw, targets)
			continue
		}
		if strings.EqualFold(key, "email") {
			collectJSONEmails(raw, targets)
			continue
		}
		var nested map[string]json.RawMessage
		if json.Unmarshal(raw, &nested) == nil {
			collectJSONTargets(nested, targets)
			continue
		}
		var nestedList []json.RawMessage
		if json.Unmarshal(raw, &nestedList) == nil {
			for _, item := range nestedList {
				var nestedObject map[string]json.RawMessage
				if json.Unmarshal(item, &nestedObject) == nil {
					collectJSONTargets(nestedObject, targets)
				}
			}
		}
	}
}

func collectJSONStrings(raw json.RawMessage, targets *[]string) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		*targets = append(*targets, value)
		return
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil {
		for _, item := range values {
			collectJSONStrings(item, targets)
		}
	}
}

func collectJSONEmails(raw json.RawMessage, targets *[]string) {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		if address, err := mail.ParseAddress(value); err == nil {
			*targets = append(*targets, address.Address)
		}
		return
	}
	var values []json.RawMessage
	if json.Unmarshal(raw, &values) == nil {
		for _, item := range values {
			collectJSONEmails(item, targets)
		}
	}
}

func isURLField(key string) bool {
	key = strings.ToLower(key)
	return key == "url" || key == "reported_url" || strings.HasSuffix(key, "_url")
}
