package server

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

func requestLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(wrapped, r)
		log.Printf("access: request_id=%s method=%q path=%q status=%d duration=%s",
			RequestIDFromContext(r.Context()), r.Method, r.URL.Path, wrapped.Status(), time.Since(started))
	})
}
