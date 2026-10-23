package cmd

import "testing"

func TestDefaultListenAddrPreservesKomariEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, shellbeam, komari, want string
	}{
		{"default", "", "", "0.0.0.0:25774"},
		{"legacy deployment", "", "127.0.0.1:25775", "127.0.0.1:25775"},
		{"ShellBeam deployment", "127.0.0.1:25776", "", "127.0.0.1:25776"},
		{"ShellBeam takes precedence", "127.0.0.1:25776", "127.0.0.1:25775", "127.0.0.1:25776"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHELLBEAM_LISTEN", tc.shellbeam)
			t.Setenv("KOMARI_LISTEN", tc.komari)
			if got := defaultListenAddr(); got != tc.want {
				t.Fatalf("listen address = %q, want %q", got, tc.want)
			}
		})
	}
}
