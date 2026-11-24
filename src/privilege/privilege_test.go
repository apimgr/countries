package privilege

import (
	"errors"
	"testing"
)

func TestSelectSystemIDUsesHighestSafeFreeID(t *testing.T) {
	uidUsed := map[int]bool{899: true, 898: true}
	gidUsed := map[int]bool{897: true}
	id, err := SelectSystemID(uidUsed, gidUsed, "linux")
	if err != nil {
		t.Fatalf("SelectSystemID() error = %v", err)
	}
	if id != 896 {
		t.Fatalf("SelectSystemID() = %d, want 896", id)
	}
}

func TestSelectSystemIDUsesMacOSRangeAndReservedIDs(t *testing.T) {
	id, err := SelectSystemID(nil, nil, "darwin")
	if err != nil {
		t.Fatalf("SelectSystemID() error = %v", err)
	}
	if id != 399 {
		t.Fatalf("SelectSystemID() = %d, want 399", id)
	}
	uidUsed := map[int]bool{}
	gidUsed := map[int]bool{}
	for value := 399; value >= 200; value-- {
		uidUsed[value] = true
	}
	if _, err := SelectSystemID(uidUsed, gidUsed, "darwin"); err == nil {
		t.Fatal("SelectSystemID() succeeded with no free ID")
	}
}

func TestSelectSystemIDRejectsExhaustedSafeRange(t *testing.T) {
	uidUsed := map[int]bool{}
	gidUsed := map[int]bool{}
	for value := 899; value >= 200; value-- {
		uidUsed[value] = true
	}
	if _, err := SelectSystemID(uidUsed, gidUsed, "linux"); err == nil {
		t.Fatal("SelectSystemID() succeeded with no free ID")
	}
}

func TestRequireElevatedRejectsUserMode(t *testing.T) {
	if err := requireElevated(1000); !errors.Is(err, ErrNotElevated) {
		t.Fatalf("requireElevated() error = %v, want ErrNotElevated", err)
	}
	if err := requireElevated(0); err != nil {
		t.Fatalf("requireElevated(root) error = %v", err)
	}
}

func TestSelectSystemIDRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := SelectSystemID(nil, nil, "windows"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("SelectSystemID(windows) error = %v, want ErrUnsupported", err)
	}
}

func TestValidSystemIDEnforcesPlatformRangesAndReservations(t *testing.T) {
	for _, test := range []struct {
		goos string
		id   int
		want bool
	}{
		{goos: "linux", id: 899, want: true},
		{goos: "linux", id: 999, want: false},
		{goos: "linux", id: 179, want: false},
		{goos: "darwin", id: 399, want: true},
		{goos: "darwin", id: 400, want: false},
	} {
		if got := validSystemID(test.id, test.goos); got != test.want {
			t.Errorf("validSystemID(%d, %q) = %t, want %t", test.id, test.goos, got, test.want)
		}
	}
}

func TestDropPrivilegesRejectsInvalidIdentity(t *testing.T) {
	for _, identity := range []Identity{
		{},
		{Name: "invalid/name", UID: 500, GID: 500},
		{Name: "countries", UID: 500, GID: 501},
		{Name: "countries", UID: 999, GID: 999},
	} {
		if err := DropPrivileges(identity); err == nil {
			t.Errorf("DropPrivileges(%+v) succeeded, want invalid identity error", identity)
		}
	}
}
