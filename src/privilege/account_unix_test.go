//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"os/user"
	"testing"
)

func TestValidateReusableAccount(t *testing.T) {
	for _, test := range []struct {
		name    string
		uid     int
		gid     int
		shell   string
		wantErr bool
	}{
		{name: "system nologin", uid: 25, gid: 25, shell: "/usr/sbin/nologin"},
		{name: "system false", uid: 999, gid: 999, shell: "/bin/false"},
		{name: "human uid", uid: 1000, gid: 1000, shell: "/usr/sbin/nologin", wantErr: true},
		{name: "root uid", uid: 0, gid: 0, shell: "/usr/sbin/nologin", wantErr: true},
		{name: "root primary group", uid: 500, gid: 0, shell: "/usr/sbin/nologin", wantErr: true},
		{name: "non-system primary group", uid: 500, gid: 1000, shell: "/usr/sbin/nologin", wantErr: true},
		{name: "login shell", uid: 500, gid: 500, shell: "/bin/sh", wantErr: true},
		{name: "unknown shell", uid: 500, gid: 500, shell: "", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateReusableAccount(test.uid, test.gid, test.shell)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateReusableAccount() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestValidIdentityForDrop(t *testing.T) {
	for _, test := range []struct {
		name     string
		identity Identity
		want     bool
	}{
		{name: "existing system account can retain distinct gid", identity: Identity{Name: "mail", UID: 8, GID: 12}, want: true},
		{name: "existing human account rejected", identity: Identity{Name: "human", UID: 1000, GID: 1000}},
		{name: "existing root group rejected", identity: Identity{Name: "service", UID: 500, GID: 0}},
		{name: "existing root user rejected", identity: Identity{Name: "root", UID: 0, GID: 0}},
		{name: "existing user group rejected", identity: Identity{Name: "service", UID: 500, GID: 1000}},
		{name: "created identity requires matching safe ids", identity: Identity{Name: "countries", UID: 500, GID: 500, Created: true}, want: true},
		{name: "created identity rejects mismatched ids", identity: Identity{Name: "countries", UID: 500, GID: 501, Created: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := validIdentityForDrop(test.identity, "linux"); got != test.want {
				t.Fatalf("validIdentityForDrop() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestPasswdShell(t *testing.T) {
	contents := "other:x:1:1::/:/bin/sh\nmail:x:8:8::/var/mail:/usr/sbin/nologin\n"
	if shell, ok := passwdShell(contents, "mail"); !ok || shell != "/usr/sbin/nologin" {
		t.Fatalf("passwdShell() = %q, %t", shell, ok)
	}
	if _, ok := passwdShell(contents, "missing"); ok {
		t.Fatal("passwdShell found missing account")
	}
}

func TestLookupAccountShellForCurrentUser(t *testing.T) {
	current, err := user.Current()
	if err != nil {
		t.Fatalf("user.Current() error = %v", err)
	}
	shell, err := lookupAccountShell(current.Username)
	if err != nil || shell == "" {
		t.Fatalf("lookupAccountShell(%q) = %q, %v", current.Username, shell, err)
	}
}
