package server

import (
	"log"
	"net/http"
	"time"

	"github.com/apimgr/countries/src/common/api"
	"github.com/go-chi/chi/v5/middleware"
)

func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		duration := time.Since(started)
		recordRequest(duration, wrapped.Status())
		if wrapped.Status() >= http.StatusOK && wrapped.Status() < http.StatusMultipleChoices && isSuccessfulHealthRequest(r.URL.Path) {
			return
		}
		log.Printf("access: request_id=%s ip=%q method=%q path=%q status=%d duration=%s",
			RequestIDFromContext(r.Context()), requestClientIP(r.RemoteAddr), r.Method, r.URL.Path, wrapped.Status(), duration)
	})
}

func isSuccessfulHealthRequest(path string) bool {
	base := api.APIBasePath()
	return path == "/server/healthz" || path == "/healthz" || path == "/api/healthz" || path == "/api/healthz.txt" || path == base+"/server/healthz" || path == base+"/server/healthz.txt"
}
