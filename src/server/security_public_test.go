package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/apimgr/countries/src/config"
	"github.com/apimgr/countries/src/testutil"
)

func TestPGPPublicKeyUnavailableAndMethodHandling(t *testing.T) {
	s := &Server{config: &config.Config{ConfigDir: testutil.TempDir(t)}}
	for _, test := range []struct {
		method string
		status int
	}{
		{method: http.MethodGet, status: http.StatusNotFound},
		{method: http.MethodPost, status: http.StatusMethodNotAllowed},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, "/.well-known/pgp-key.asc", nil)
		s.handlePGPPublicKey(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("%s status = %d, want %d", test.method, recorder.Code, test.status)
		}
	}
}

func TestSecurityReportBodyUsesPGPWhenPublicKeyExists(t *testing.T) {
	configDir := testutil.TempDir(t)
	securityDir := filepath.Join(configDir, "security")
	if err := os.MkdirAll(securityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	entity, err := openpgp.NewEntity("Countries Security", "", "security@example.test", nil)
	if err != nil {
		t.Fatalf("generate test PGP key: %v", err)
	}
	var armored bytes.Buffer
	writer, err := armor.Encode(&armored, openpgp.PublicKeyType, nil)
	if err != nil {
		t.Fatalf("armor test PGP public key: %v", err)
	}
	if err := entity.Serialize(writer); err != nil {
		t.Fatalf("serialize test PGP public key: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("finish test PGP public key: %v", err)
	}
	if err := os.WriteFile(filepath.Join(securityDir, "pgp.pub.asc"), armored.Bytes(), 0o600); err != nil {
		t.Fatalf("write test PGP public key: %v", err)
	}

	s := &Server{config: &config.Config{ConfigDir: configDir}}
	plain := []byte(`{"summary":"private vulnerability detail"}`)
	ciphertext, algorithm, err := s.encryptSecurityReportBody(plain)
	if err != nil {
		t.Fatalf("encrypt security report: %v", err)
	}
	if algorithm != "pgp" {
		t.Fatalf("body algorithm = %q, want pgp", algorithm)
	}
	if len(ciphertext) == 0 || bytes.Contains(ciphertext, plain) {
		t.Fatal("security report was not encrypted")
	}
}

func TestSecurityPolicyAPIUsesConfiguredMarkdown(t *testing.T) {
	s := &Server{config: &config.Config{Web: config.WebConfig{Security: config.WebDisclosureConfig{Policy: "# Custom policy"}}}}
	recorder := httptest.NewRecorder()
	s.handleSecurityPolicyAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/server/security/policy", nil))
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Policy string `json:"policy"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode policy response: %v", err)
	}
	if !response.OK || response.Data.Policy != "# Custom policy" {
		t.Fatalf("policy response = %#v", response)
	}
}

func TestSecurityThanksAPIReturnsEmptyEntriesWithoutDatabase(t *testing.T) {
	s := &Server{}
	recorder := httptest.NewRecorder()
	s.handleSecurityThanksAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/server/security/thanks", nil))
	var response struct {
		OK   bool `json:"ok"`
		Data struct {
			Entries []map[string]string `json:"entries"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode thanks response: %v", err)
	}
	if recorder.Code != http.StatusOK || !response.OK || response.Data.Entries == nil || len(response.Data.Entries) != 0 {
		t.Fatalf("thanks response = %d %s", recorder.Code, recorder.Body.String())
	}
}
