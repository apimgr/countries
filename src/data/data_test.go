package data

import "testing"

// ReadFile must return the embedded countries.json fixture that ships
// with the binary.
func TestReadFile_Success(t *testing.T) {
	got, err := ReadFile("countries.json")
	if err != nil {
		t.Fatalf("ReadFile(countries.json) unexpected error: %v", err)
	}
	if len(got) == 0 {
		t.Error("ReadFile(countries.json) returned empty data")
	}
}

// A file that was never embedded must return an error, not empty data.
func TestReadFile_NotFound(t *testing.T) {
	_, err := ReadFile("does-not-exist.json")
	if err == nil {
		t.Error("ReadFile(does-not-exist.json) expected error, got nil")
	}
}
