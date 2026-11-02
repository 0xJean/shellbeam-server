package plugin

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/messageSender"
)

func TestServeConfinedRefusesLinksOutOfTheDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "plugin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(base, "komari.db")
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "page.html"), []byte("page"), 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"absolute.html": secret,
		"relative.html": "../komari.db",
		"inside.html":   "page.html",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	// Reads clean to a path inside the directory, resolves to one outside it.
	if err := os.Symlink(".", filepath.Join(dir, "self")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../self/../komari.db", filepath.Join(dir, "a", "tricky.html")); err != nil {
		t.Fatal(err)
	}

	cases := map[string]int{
		"page.html":     http.StatusOK,
		"inside.html":   http.StatusOK,
		"absolute.html": http.StatusNotFound,
		"relative.html": http.StatusNotFound,
		"a/tricky.html": http.StatusNotFound,
		"a":             http.StatusNotFound,
		"missing.html":  http.StatusNotFound,
	}
	for name, want := range cases {
		rec := httptest.NewRecorder()
		serveConfined(rec, httptest.NewRequest(http.MethodGet, "/", nil), dir, filepath.Join(dir, name))
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d", name, rec.Code, want)
		}
		if strings.Contains(rec.Body.String(), "host secret") {
			t.Errorf("%s: served the host file", name)
		}
	}
}

func TestReadConfinedRefusesLinksOutOfTheDirectory(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "plugin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret", filepath.Join(dir, "script.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := readConfined(dir, "script.js"); err == nil {
		t.Fatal("read a file outside the plugin directory")
	}
}

func TestInstallZipExpectingLeavesTheInstalledPluginAlone(t *testing.T) {
	withTempDataDir(t)
	manifest := `{"name":"Victim","short":"victim","version":"1.0.0","permissions":{"timeout":5}}`
	if _, err := InstallZip(writePluginZip(t, map[string]string{
		"komari-plugin.json": manifest,
		"script.js":          `// original`,
	})); err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("not the catalog entry")
	_, err := InstallZipExpecting(writePluginZip(t, map[string]string{
		"komari-plugin.json": manifest,
		"script.js":          `// replaced`,
	}), func(info models.Plugin) error {
		if info.Short != "other" {
			return rejected
		}
		return nil
	})
	if !errors.Is(err, rejected) {
		t.Fatalf("err = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(DataDir, "victim", "script.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `// original` {
		t.Fatalf("installed plugin was overwritten: %q", data)
	}
}

func TestPluginChannelsNeverSeeAgentTokens(t *testing.T) {
	original := messageSender.Notification{Event: models.EventMessage{
		Clients: []models.Client{{UUID: "a", Token: "secret"}},
	}}
	stripped := withoutClientTokens(original)
	if stripped.Event.Clients[0].Token != "" || stripped.Event.Clients[0].UUID != "a" {
		t.Fatalf("stripped = %+v", stripped.Event.Clients[0])
	}
	if original.Event.Clients[0].Token != "secret" {
		t.Fatal("other channels share the event and must keep it unchanged")
	}
}

func TestInstalledFileRouteRefusesPlantedLink(t *testing.T) {
	withTempDataDir(t)
	if _, err := InstallZip(writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"Link","short":"link","version":"1.0.0","permissions":{"timeout":5}}`,
		"script.js":          `// plugin`,
	})); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "komari.db")
	if err := os.WriteFile(secret, []byte("host secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(DataDir, "link", "page.html")); err != nil {
		t.Fatal(err)
	}
	full, err := ResolveFile("link", "page.html")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	ServeInstalledFile(rec, httptest.NewRequest(http.MethodGet, "/", nil), "link", full)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "host secret") {
		t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
	}
}
