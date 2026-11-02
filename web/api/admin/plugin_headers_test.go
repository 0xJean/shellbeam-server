package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/plugin"
)

func TestServePluginFileIsSandboxed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := plugin.DataDir
	plugin.DataDir = t.TempDir()
	defer func() { plugin.DataDir = previous }()
	dir := filepath.Join(plugin.DataDir, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"komari-plugin.json": `{"name":"Demo","short":"demo","version":"1.0.0"}`,
		"page.html":          `<script>1</script>`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r := gin.New()
	r.GET("/plugin/:short/*filepath", ServePluginFile)
	response := httptest.NewRecorder()
	r.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/plugin/demo/page.html", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Security-Policy"); got != "sandbox allow-scripts" {
		t.Fatalf("CSP = %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("nosniff = %q", got)
	}
}
