package app

import (
	"reflect"
	"testing"
)

func TestDaemonizationPolicy(t *testing.T) {
	tests := []struct {
		name       string
		flag       bool
		configured bool
		service    bool
		container  bool
		managed    bool
		child      bool
		want       bool
	}{
		{name: "default foreground"},
		{name: "flag", flag: true, want: true},
		{name: "config", configured: true, want: true},
		{name: "service command", flag: true, service: true},
		{name: "container", flag: true, container: true},
		{name: "service manager", flag: true, managed: true},
		{name: "daemon child", flag: true, child: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldDaemonize(test.flag, test.configured, test.service, test.container, test.managed, test.child); got != test.want {
				t.Errorf("shouldDaemonize() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDaemonChildArgsAndEnvironment(t *testing.T) {
	if got, want := daemonChildArgs([]string{"--port", "80", "--daemon", "--daemon=true", "--help"}), []string{"--port", "80", "--help"}; !reflect.DeepEqual(got, want) {
		t.Errorf("daemonChildArgs() = %#v, want %#v", got, want)
	}
	if got, want := daemonChildEnvironment([]string{"PATH=/bin", daemonChildMarker + "=old"}), []string{"PATH=/bin", daemonChildMarker + "=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("daemonChildEnvironment() = %#v, want %#v", got, want)
	}
}
