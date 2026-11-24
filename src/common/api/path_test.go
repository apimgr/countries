package api

import "testing"

func TestAPIPathUsesVersionedBase(t *testing.T) {
	if got := APIBasePath(); got != "/api/v1" {
		t.Fatalf("APIBasePath() = %q", got)
	}
	for _, test := range []struct {
		resource string
		want     string
	}{
		{"countries", "/api/v1/countries"},
		{"/server/healthz", "/api/v1/server/healthz"},
		{"", "/api/v1/"},
	} {
		if got := APIPath(test.resource); got != test.want {
			t.Errorf("APIPath(%q) = %q, want %q", test.resource, got, test.want)
		}
	}
}
