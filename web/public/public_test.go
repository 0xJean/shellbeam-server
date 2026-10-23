package public

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNormalizeHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"hyphen language": {
			input: "zh-CN",
			want:  "zh-CN",
		},
		"underscore language": {
			input: "zh_CN",
			want:  "zh-CN",
		},
		"reject script injection": {
			input: `zh-CN" autofocus`,
		},
		"reject too short": {
			input: "z",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := normalizeHTMLLanguage(tt.input); got != tt.want {
				t.Fatalf("normalizeHTMLLanguage(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceHTMLLanguage(t *testing.T) {
	tests := map[string]struct {
		html     string
		language string
		want     string
	}{
		"replace existing lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: "zh-CN",
			want:     `<html lang="zh-CN"><head></head></html>`,
		},
		"insert missing lang": {
			html:     `<html><head></head></html>`,
			language: "ja_JP",
			want:     `<html lang="ja-JP"><head></head></html>`,
		},
		"ignore invalid lang": {
			html:     `<html lang="en"><head></head></html>`,
			language: `zh-CN" autofocus`,
			want:     `<html lang="en"><head></head></html>`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := replaceHTMLLanguage(tt.html, tt.language); got != tt.want {
				t.Fatalf("replaceHTMLLanguage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEmbeddedDistNeedsNoPreparation(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := PrepareDefaultDist(); err != nil {
		t.Fatalf("prepare embedded dist: %v", err)
	}
	for _, name := range []string{"index.html", "style.css", "favicon.svg"} {
		content, err := PublicFS.ReadFile("defaultTheme/dist/" + name)
		if err != nil || len(content) == 0 {
			t.Fatalf("embedded dist is missing non-empty %s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read working directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("preparing embedded assets created files: %v", entries)
	}

	content, err := PublicFS.ReadFile("defaultTheme/komari-theme.json")
	if err != nil {
		t.Fatalf("read default theme manifest: %v", err)
	}
	var manifest struct {
		Name  string `json:"name"`
		Short string `json:"short"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("parse default theme manifest: %v", err)
	}
	if manifest.Name != "ShellBeam Server" || manifest.Short != DefaultTheme {
		t.Fatalf("default theme metadata = %+v", manifest)
	}
}

func TestStaticServesEmbeddedAssetsWithoutPreparation(t *testing.T) {
	t.Chdir(t.TempDir())
	setupStaticConfig(t)
	router := staticTestRouter(false)
	for _, requestPath := range []string{"/", "/admin", "/terminal/client", "/database-recovery"} {
		t.Run(requestPath, func(t *testing.T) {
			recorder := serveStaticRequest(router, requestPath)
			if recorder.Code != http.StatusOK {
				t.Fatalf("index status = %d, want 200", recorder.Code)
			}
			if !strings.Contains(recorder.Body.String(), "<title>ShellBeam Server</title>") ||
				!strings.Contains(recorder.Body.String(), "Seeing this page does not confirm that setup is complete.") {
				t.Fatalf("index does not explain the ShellBeam connection and setup boundary: %s", recorder.Body.String())
			}
		})
	}
	for requestPath, filename := range map[string]string{
		"/style.css":                       "style.css",
		"/themes/default/dist/style.css":   "style.css",
		"/themes/default/dist/favicon.svg": "favicon.svg",
		"/favicon.ico":                     "favicon.svg",
	} {
		recorder := serveStaticRequest(router, requestPath)
		want, err := PublicFS.ReadFile("defaultTheme/dist/" + filename)
		if err != nil {
			t.Fatal(err)
		}
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), want) {
			t.Fatalf("asset %s does not match its embedded content: status %d", requestPath, recorder.Code)
		}
	}
}

func TestStaticRestrictedDoesNotServeCustomAssetOverride(t *testing.T) {
	t.Chdir(t.TempDir())
	setupStaticConfig(t)
	writeCustomStaticFile(t, "data/theme/custom/dist/style.css", "custom override")
	writeCustomStaticFile(t, "data/theme/custom/dist/index.html", "custom index override")
	writeCustomStaticFile(t, "data/favicon.ico", "custom favicon override")
	if err := config.SetMany(map[string]any{
		config.ThemeKey:      "custom",
		config.CustomHeadKey: "<script>custom head override</script>",
		config.CustomBodyKey: "custom body override",
	}); err != nil {
		t.Fatalf("set custom theme config: %v", err)
	}

	router := staticTestRouter(true)
	wantCSS, err := PublicFS.ReadFile("defaultTheme/dist/style.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, requestPath := range []string{"/style.css", "/themes/default/dist/style.css"} {
		recorder := serveStaticRequest(router, requestPath)
		if recorder.Code != http.StatusOK || !bytes.Equal(recorder.Body.Bytes(), wantCSS) {
			t.Fatalf("restricted asset %s does not match its embedded content: status %d", requestPath, recorder.Code)
		}
	}
	for _, requestPath := range []string{"/themes/custom/dist/style.css", "/themes/custom/dist/index.html"} {
		recorder := serveStaticRequest(router, requestPath)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("restricted asset %s status = %d, want 404", requestPath, recorder.Code)
		}
	}
	wantFavicon, err := PublicFS.ReadFile("defaultTheme/dist/favicon.svg")
	if err != nil {
		t.Fatal(err)
	}
	faviconRecorder := serveStaticRequest(router, "/favicon.ico")
	if faviconRecorder.Code != http.StatusOK || !bytes.Equal(faviconRecorder.Body.Bytes(), wantFavicon) {
		t.Fatalf("restricted favicon does not match its embedded content: status %d", faviconRecorder.Code)
	}
	for _, requestPath := range []string{"/database-recovery", "/admin", "/install"} {
		recorder := serveStaticRequest(router, requestPath)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Connect with") {
			t.Fatalf("restricted index %s is not the embedded connection page: status %d", requestPath, recorder.Code)
		}
		for _, forbidden := range []string{"custom index override", "custom head override", "custom body override", "vite-plugin-pwa:register-sw"} {
			if strings.Contains(recorder.Body.String(), forbidden) {
				t.Fatalf("restricted index %s contains %q", requestPath, forbidden)
			}
		}
	}
}

func TestStaticRetainsCustomThemeAndConfigCompatibility(t *testing.T) {
	t.Chdir(t.TempDir())
	setupStaticConfig(t)
	writeCustomStaticFile(t, "data/theme/custom/dist/index.html", `<html lang="en"><head><title>Komari Monitor</title><meta name="description" content="A simple server monitor tool."></head><body>custom index</body></html>`)
	writeCustomStaticFile(t, "data/theme/custom/dist/style.css", "custom stylesheet")
	if err := config.SetMany(map[string]any{
		config.ThemeKey:       "custom",
		config.SitenameKey:    "Existing server name",
		config.DescriptionKey: "Existing description",
		config.CustomHeadKey:  "<meta name=\"custom\" content=\"head\">",
		config.CustomBodyKey:  "custom body",
	}); err != nil {
		t.Fatalf("set custom config: %v", err)
	}

	router := staticTestRouter(false)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: LanguageCookieName, Value: "zh_CN"})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	for _, want := range []string{`<html lang="zh-CN">`, "<title>Existing server name</title>", "Existing description", "custom index", `name="custom"`, "custom body"} {
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), want) {
			t.Fatalf("custom index missing %q: status %d, body %s", want, recorder.Code, recorder.Body.String())
		}
	}
	for _, requestPath := range []string{"/style.css", "/themes/custom/dist/style.css"} {
		recorder := serveStaticRequest(router, requestPath)
		if recorder.Code != http.StatusOK || recorder.Body.String() != "custom stylesheet" {
			t.Fatalf("custom asset %s = %q (status %d)", requestPath, recorder.Body.String(), recorder.Code)
		}
	}
	for _, requestPath := range []string{"/admin", "/terminal/client"} {
		recorder := serveStaticRequest(router, requestPath)
		if recorder.Code != http.StatusOK || strings.Contains(recorder.Body.String(), "custom index") || strings.Contains(recorder.Body.String(), "custom body") {
			t.Fatalf("system path %s served a custom override: status %d", requestPath, recorder.Code)
		}
	}
	if got, err := config.GetAs[string](config.SitenameKey); err != nil || got != "Existing server name" {
		t.Fatalf("existing site name was changed: got %q, err %v", got, err)
	}
}

func setupStaticConfig(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open config db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("open SQL config db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	config.SetDb(db)
}

func staticTestRouter(restricted bool) *gin.Engine {
	router := gin.New()
	static(router.Group("/"), router.NoRoute, restricted)
	return router
}

func serveStaticRequest(router *gin.Engine, requestPath string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, requestPath, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func writeCustomStaticFile(t *testing.T, filename, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatalf("create custom asset directory: %v", err)
	}
	if err := os.WriteFile(filename, []byte(content), 0o644); err != nil {
		t.Fatalf("write custom asset: %v", err)
	}
}
