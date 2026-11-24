package server

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBlocklistTargetsReadsRequestValuesAndRestoresBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/contact?next_url=https%3A%2F%2Fquery.example%2F", strings.NewReader("email=person%40mail.example&reported_url=https%3A%2F%2Freported.example%2F"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "https://origin.example")
	targets, tooLarge, err := blocklistTargets(request)
	if err != nil || tooLarge {
		t.Fatalf("blocklistTargets() error = %v, tooLarge = %v", err, tooLarge)
	}
	for _, want := range []string{"https://origin.example", "https://query.example/", "person@mail.example", "https://reported.example/"} {
		if !blocklistContains(targets, want) {
			t.Errorf("targets %q do not contain %q", targets, want)
		}
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || string(body) != "email=person%40mail.example&reported_url=https%3A%2F%2Freported.example%2F" {
		t.Fatalf("request body after inspection = %q, %v", body, err)
	}
}

func TestBlocklistTargetsUsesRefererWhenOriginAbsentAndScansJSON(t *testing.T) {
	request := httptest.NewRequest("POST", "/api", strings.NewReader(`{"contact":{"email":"person@mail.example"},"url":"https://target.example/path"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Referer", "https://referer.example/page")
	targets, tooLarge, err := blocklistTargets(request)
	if err != nil || tooLarge {
		t.Fatalf("blocklistTargets() error = %v, tooLarge = %v", err, tooLarge)
	}
	for _, want := range []string{"https://referer.example/page", "person@mail.example", "https://target.example/path"} {
		if !blocklistContains(targets, want) {
			t.Errorf("targets %q do not contain %q", targets, want)
		}
	}
}

func TestBlocklistTargetsRejectsOversizeBody(t *testing.T) {
	request := httptest.NewRequest("POST", "/api", strings.NewReader(strings.Repeat("x", blocklistRequestBodyLimit+1)))
	request.Header.Set("Content-Type", "application/json")
	_, tooLarge, err := blocklistTargets(request)
	if err != nil || !tooLarge {
		t.Fatalf("blocklistTargets() tooLarge = %v, error = %v", tooLarge, err)
	}
}

func blocklistContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
