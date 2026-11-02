package process

import "testing"

func TestSafeEnvironmentDropsSecrets(t *testing.T) {
	got := safeEnvironment([]string{
		"PATH=/bin", "TZ=UTC", "LC_ALL=C", "HOME=/app",
		"KOMARI_DB_PASS=secret", "AWS_SECRET_ACCESS_KEY=secret", "METRIC_DB_DSN=mysql://u:p@h/d", "LCX=no", "NOVALUE",
	})
	if len(got) != 4 || got["PATH"] != "/bin" || got["TZ"] != "UTC" || got["LC_ALL"] != "C" || got["HOME"] != "/app" {
		t.Fatalf("environment = %v", got)
	}
}
