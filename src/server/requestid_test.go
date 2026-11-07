package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestIDMiddlewareGeneratesAndValidatesIDs(t *testing.T) {
	valid := "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name      string
		requestID string
		wantID    string
	}{
		{name: "generated"},
		{name: "accepted", requestID: valid, wantID: valid},
		{name: "invalid replaced", requestID: "not-a-uuid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if RequestIDFromContext(r.Context()) != w.Header().Get("X-Request-ID") {
					t.Error("request ID was not added to the request context")
				}
			})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if test.requestID != "" {
				request.Header.Set("X-Request-ID", test.requestID)
			}
			requestIDMiddleware(next).ServeHTTP(recorder, request)
			got := recorder.Header().Get("X-Request-ID")
			if got == "" {
				t.Fatal("response did not include X-Request-ID")
			}
			if test.wantID != "" && got != test.wantID {
				t.Fatalf("X-Request-ID = %q, want %q", got, test.wantID)
			}
			if !validUUIDv4(got) {
				t.Fatalf("X-Request-ID %q is not a UUID v4", got)
			}
		})
	}
}

func TestValidUUIDv4(t *testing.T) {
	for _, value := range []string{
		"550e8400-e29b-11d4-a716-446655440000",
		"550e8400-e29b-41d4-6716-446655440000",
		"invalid",
	} {
		if validUUIDv4(value) {
			t.Errorf("validUUIDv4(%q) = true, want false", value)
		}
	}
	if !validUUIDv4("550e8400-e29b-41d4-a716-446655440000") {
		t.Fatal("valid UUID v4 was rejected")
	}
}
