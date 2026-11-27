package service

import (
	"strings"
	"testing"
)

func TestPrivateACLDescriptorProtectsOnlyApprovedPrincipals(t *testing.T) {
	const serviceSID = "S-1-5-80-123-456-789-101-112"
	got, err := privateACLDescriptor(serviceSID)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"D:P",
		"(A;OICI;FA;;;" + serviceSID + ")",
		"(A;OICI;FA;;;SY)",
		"(A;OICI;FA;;;BA)",
	} {
		if !strings.Contains(got, expected) {
			t.Errorf("ACL descriptor %q is missing %q", got, expected)
		}
	}
	if strings.Contains(got, "BU") || strings.Contains(got, "WD") {
		t.Fatalf("ACL descriptor grants access to an unapproved principal: %q", got)
	}
}

func TestPrivateACLDescriptorRejectsNonServiceSID(t *testing.T) {
	if _, err := privateACLDescriptor("S-1-5-32-545"); err == nil {
		t.Fatal("privateACLDescriptor accepted a non-service SID")
	}
}

func TestEnforcePrivateACLIsNoOpInUserMode(t *testing.T) {
	if err := EnforcePrivateACL(false, t.TempDir(), t.TempDir()); err != nil {
		t.Fatalf("EnforcePrivateACL() in user mode error = %v", err)
	}
}
