package server

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
)

const buildCookieName = "countries_build"

func assetStamp(version, commit string) string {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		commit = "unknown"
	}
	if len(commit) > 7 {
		commit = commit[:7]
	}
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	return version + "-" + commit
}

func (s *Server) assetStamp() string {
	return assetStamp(s.version, s.commit)
}

func (s *Server) setHTMLResponseHeaders(w http.ResponseWriter, r *http.Request, status int) {
	stamp := s.assetStamp()
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("ETag", fmt.Sprintf("%q", stamp))
	if status >= http.StatusOK && status < http.StatusMultipleChoices &&
		r.Method != http.MethodHead && isTopLevelDocumentNavigation(r) {
		if cookie, err := r.Cookie(buildCookieName); err == nil && cookie.Value != stamp {
			w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
		}
	}
	http.SetCookie(w, &http.Cookie{
		Name: buildCookieName, Value: stamp, Path: "/", MaxAge: 31536000,
		Secure: true, SameSite: http.SameSiteLaxMode,
	})
}

func isTopLevelDocumentNavigation(r *http.Request) bool {
	destination := r.Header.Get("Sec-Fetch-Dest")
	mode := r.Header.Get("Sec-Fetch-Mode")
	if destination == "" && mode == "" {
		return true
	}
	return destination == "document" && mode == "navigate"
}

func (s *Server) setAssetCacheHeaders(w http.ResponseWriter, r *http.Request) bool {
	stamp := s.assetStamp()
	pathHash := sha256.Sum256([]byte(r.URL.Path))
	etag := fmt.Sprintf("%q", stamp+"-"+hex.EncodeToString(pathHash[:8]))
	w.Header().Set("ETag", etag)
	if r.URL.Query().Get("v") == stamp {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	return r.Header.Get("If-None-Match") == etag
}

func (s *Server) setDynamicAssetCacheHeaders(w http.ResponseWriter) {
	stamp := s.assetStamp()
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", fmt.Sprintf("%q", stamp))
}
