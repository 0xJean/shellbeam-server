package install

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/internal/metricstore"
	"github.com/komari-monitor/komari/database/models"
	appconfig "github.com/komari-monitor/komari/internal/config"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupInstallRouter(t *testing.T) (*gin.Engine, *gorm.DB, *Controller) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "install.db"))+"?mode=rwc"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open install database: %v", err)
	}
	if err := db.AutoMigrate(&models.User{}, &appconfig.ConfigItem{}); err != nil {
		t.Fatalf("migrate install database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get install sql database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	appconfig.SetDb(db)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	controller := NewController(db)
	controller.Activate()
	controller.Register(r)
	return r, db, controller
}

func performJSON(r http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	encoded, _ := json.Marshal(body)
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Host = "127.0.0.1:25774"
	request.RemoteAddr = "127.0.0.1:54321"
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func TestInstallRejectsInvalidInputWithoutCreatingAccount(t *testing.T) {
	r, db, _ := setupInstallRouter(t)
	response := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username: "admin", Password: "short", Sitename: "Komari",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid install status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("invalid install created users: count=%d err=%v", count, err)
	}
}

func TestInstallRejectsWeakPasswordWithoutCreatingAccount(t *testing.T) {
	r, db, _ := setupInstallRouter(t)
	response := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username: "admin", Password: "lowercaseonly1", Sitename: "Komari", MetricDSN: "./data/metrics.db",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("weak password status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("weak password created users: count=%d err=%v", count, err)
	}
}

func TestInstallCompletesAndPersistsSettings(t *testing.T) {
	r, db, _ := setupInstallRouter(t)
	metricDSN := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "metrics.db")) + "?mode=rwc"
	response := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username:    "owner",
		Password:    "Correct-horse-battery-staple1",
		Sitename:    "My Komari",
		Description: "Private monitoring",
		MetricDSN:   metricDSN,
	})
	if response.Code != http.StatusOK {
		t.Fatalf("complete install status = %d: %s", response.Code, response.Body.String())
	}
	var user models.User
	if err := db.First(&user, "username = ?", "owner").Error; err != nil {
		t.Fatalf("find installed admin: %v", err)
	}
	want := map[string]any{
		appconfig.SitenameKey:         "My Komari",
		appconfig.DescriptionKey:      "Private monitoring",
		metricstore.MetricDBDriverKey: "sqlite",
		metricstore.MetricDBDSNKey:    metricDSN,
	}
	got, err := appconfig.GetAll()
	if err != nil {
		t.Fatalf("read all install settings: %v", err)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("setting %s = %#v, want %#v", key, got[key], value)
		}
	}

	repeat := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username: "other", Password: "Another-password1", Sitename: "Other", MetricDSN: "./data/metrics.db",
	})
	if repeat.Code != http.StatusConflict {
		t.Fatalf("repeat install status = %d, want %d", repeat.Code, http.StatusConflict)
	}
}

func TestInstallRejectsUnknownDSN(t *testing.T) {
	r, db, _ := setupInstallRouter(t)
	response := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username: "admin", Password: "Strong-password1", Sitename: "Komari",
		MetricDSN: "not-a-recognized-dsn",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown DSN status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var count int64
	if err := db.Model(&models.User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed DSN created users: count=%d err=%v", count, err)
	}
}

// installRequest posts an invalid body, so a request that passes the guards
// stops at validation with 400 and creates nothing.
func installRequest(remoteAddr string, headers map[string]string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, APIPath+"/complete", strings.NewReader(`{"username":"admin"}`))
	request.Host = "127.0.0.1:25774"
	request.RemoteAddr = remoteAddr
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if name == "Host" {
			request.Host = value
			continue
		}
		request.Header.Set(name, value)
	}
	return request
}

func TestInstallGuards(t *testing.T) {
	r, _, controller := setupInstallRouter(t)
	token := controller.setupToken
	if token == "" {
		t.Fatal("activation did not generate a setup token")
	}
	const remote, loopback = "203.0.113.9:4000", "127.0.0.1:4000"
	cases := []struct {
		name    string
		remote  string
		headers map[string]string
		want    int
	}{
		{"remote without token", remote, nil, http.StatusUnauthorized},
		{"remote with wrong token", remote, map[string]string{SetupTokenHeader: "wrong"}, http.StatusUnauthorized},
		{"remote with token", remote, map[string]string{SetupTokenHeader: token}, http.StatusBadRequest},
		{"remote claiming loopback", remote, map[string]string{"X-Forwarded-For": "127.0.0.1", "X-Real-IP": "127.0.0.1"}, http.StatusUnauthorized},
		{"ipv4 loopback", loopback, nil, http.StatusBadRequest},
		{"ipv6 loopback", "[::1]:4000", nil, http.StatusBadRequest},
		{"mapped loopback", "[::ffff:127.0.0.1]:4000", nil, http.StatusBadRequest},
		{"proxied X-Forwarded-For", loopback, map[string]string{"X-Forwarded-For": "203.0.113.9"}, http.StatusUnauthorized},
		{"proxied X-Forwarded-Proto", loopback, map[string]string{"X-Forwarded-Proto": "https"}, http.StatusUnauthorized},
		{"proxied Forwarded", loopback, map[string]string{"Forwarded": "for=203.0.113.9"}, http.StatusUnauthorized},
		{"proxied with token", loopback, map[string]string{"X-Forwarded-For": "203.0.113.9", SetupTokenHeader: token}, http.StatusBadRequest},
		{"foreign origin on loopback", loopback, map[string]string{"Origin": "https://evil.example"}, http.StatusForbidden},
		{"null origin on loopback", loopback, map[string]string{"Origin": "null"}, http.StatusForbidden},
		{"same origin on loopback", loopback, map[string]string{"Origin": "http://127.0.0.1:25774"}, http.StatusBadRequest},
		{"localhost host", loopback, map[string]string{"Host": "LOCALHOST:25774"}, http.StatusBadRequest},
		{"ipv6 host", "[::1]:4000", map[string]string{"Host": "[::1]:25774"}, http.StatusBadRequest},
		{"rebound host", loopback, map[string]string{"Host": "evil.example:25774"}, http.StatusUnauthorized},
		{"rebound host with origin", loopback, map[string]string{"Host": "evil.example:25774", "Origin": "http://evil.example:25774"}, http.StatusUnauthorized},
		{"lookalike host", loopback, map[string]string{"Host": "localhost.evil.example"}, http.StatusUnauthorized},
		{"other loopback port", loopback, map[string]string{"Origin": "http://127.0.0.1:9999"}, http.StatusForbidden},
		{"non-web origin scheme", loopback, map[string]string{"Origin": "ftp://127.0.0.1:25774"}, http.StatusUnauthorized},
		{"proxied Via", loopback, map[string]string{"Via": "1.1 proxy"}, http.StatusUnauthorized},
		{"public host with token", loopback, map[string]string{"Host": "monitor.example", "Origin": "https://monitor.example", SetupTokenHeader: token}, http.StatusBadRequest},
		{"form content type", loopback, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, http.StatusUnsupportedMediaType},
		{"plain content type", loopback, map[string]string{"Content-Type": "text/plain"}, http.StatusUnsupportedMediaType},
		{"json with charset", loopback, map[string]string{"Content-Type": "application/json; charset=utf-8"}, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			r.ServeHTTP(response, installRequest(tc.remote, tc.headers))
			if response.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.want, response.Body.String())
			}
		})
	}
}

func TestInstallUploadRequiresTokenAndStatusStaysOpen(t *testing.T) {
	r, _, _ := setupInstallRouter(t)
	for _, operation := range []string{"init", "chunk", "merge", "cancel"} {
		request := httptest.NewRequest(http.MethodPost, APIPath+"/upload/"+operation, strings.NewReader(`{}`))
		request.RemoteAddr = "203.0.113.9:4000"
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("upload %s status = %d, want %d", operation, response.Code, http.StatusUnauthorized)
		}
	}
	request := httptest.NewRequest(http.MethodGet, APIPath+"/status", nil)
	request.RemoteAddr = "203.0.113.9:4000"
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status endpoint = %d, want %d", response.Code, http.StatusOK)
	}
}

func TestInstallConnectionErrorHidesCredentials(t *testing.T) {
	r, _, _ := setupInstallRouter(t)
	const secret = "s3cret-password"
	response := performJSON(r, http.MethodPost, APIPath+"/complete", completeRequest{
		Username:  "admin",
		Password:  "Correct-horse-battery-staple1",
		Sitename:  "Komari",
		MetricDSN: "postgres://owner:" + secret + "@127.0.0.1:1/metrics?connect_timeout=1",
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("response leaked the DSN password: %s", response.Body.String())
	}
}
