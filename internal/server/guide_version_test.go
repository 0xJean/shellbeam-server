package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/utils"
	installweb "github.com/komari-monitor/komari/web/install"
)

func TestInstallGuidePublishesIdentityBeforeCredentials(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// Identity and installation status do not require a database connection.
	controller := installweb.NewController(nil)
	controller.Activate()
	t.Cleanup(controller.Deactivate)
	app := New(Options{})
	router := app.newGuideEngine(controller, guideServerConfig{
		pagePath: installweb.PagePath, missingAPI: "Not found in install mode",
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/version", nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("version status=%d cache=%q: %s", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}
	var version struct {
		Status string            `json:"status"`
		Data   utils.VersionInfo `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &version); err != nil {
		t.Fatal(err)
	}
	if version.Status != "success" || version.Data != utils.GetVersionInfo() || version.Data.Product != "ShellBeam Server" || version.Data.Version != "1.6.0" {
		t.Fatalf("unexpected first-run identity: %+v", version)
	}

	status := httptest.NewRecorder()
	router.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/install/status", nil))
	var installation struct {
		Data installweb.Status `json:"data"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &installation); err != nil {
		t.Fatal(err)
	}
	if status.Code != http.StatusOK || !installation.Data.Required || installation.Data.State != "ready" {
		t.Fatalf("identity read changed installation state: %s", status.Body.String())
	}

	for _, request := range []struct{ method, path string }{
		{http.MethodPost, "/api/version"},
		{http.MethodPost, "/api/login"},
		{http.MethodGet, "/api/clients"},
		{http.MethodGet, "/api/admin/settings"},
		{http.MethodPost, "/api/rpc2"},
	} {
		t.Run(request.method+request.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(request.method, request.path, nil))
			if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("normal API exposed in install mode: status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
