package router

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/web/api"
	"github.com/pquerna/otp/totp"
)

func TestMain(m *testing.M) {
	flags.DatabaseType = flags.DatabaseTypeSQLite
	flags.DatabaseFile = "file:web_router_test?mode=memory&cache=shared"
	db := dbcore.GetDBInstance()
	if sqlDB, err := db.DB(); err == nil {
		sqlDB.SetMaxOpenConns(1)
	}
	os.Exit(m.Run())
}

func routeMethods(r *gin.Engine, path string) map[string]bool {
	methods := map[string]bool{}
	for _, route := range r.Routes() {
		if route.Path == path {
			methods[route.Method] = true
		}
	}
	return methods
}

func TestExternalAccountBindIsPostOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)
	methods := routeMethods(r, "/api/admin/oauth2/bind")
	if !methods["POST"] || len(methods) != 1 {
		t.Fatalf("bind route methods = %v, want only POST", methods)
	}
}

func TestSensitiveRESTRoutesChallengeOnly2FAAccounts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	Register(r)

	const apiKey = "router-test-api-key"
	if err := config.Set(config.ApiKeyKey, apiKey); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	session := func(secret string) string {
		user, err := accounts.CreateAccount("router-"+uuid.NewString(), "Password123")
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if secret != "" {
			if err := accounts.Enable2Fa(user.UUID, secret); err != nil {
				t.Fatalf("enable 2fa: %v", err)
			}
		}
		value, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
		if err != nil {
			t.Fatalf("create session: %v", err)
		}
		return value
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "test"})
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	twoFactorSession, plainSession := session(key.Secret()), session("")

	// The bodies are invalid on purpose: passing the challenge ends in a 400
	// from the handler, before anything is installed or uploaded.
	routes := []struct{ method, path, body string }{
		{http.MethodPost, "/api/admin/upload/init", `{"purpose":"unsupported","size":1}`},
		{http.MethodPost, "/api/admin/plugin/market/install", `{}`},
		{http.MethodPost, "/api/admin/client/missing/file/upload", `{}`},
		{http.MethodGet, "/api/admin/client/missing/file/preview-token", ""},
	}
	perform := func(method, path, body, cookie, bearer, code string) int {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: "session_token", Value: cookie})
		}
		if bearer != "" {
			request.Header.Set("Authorization", "Bearer "+bearer)
		}
		if code != "" {
			request.Header.Set("X-2FA-Code", code)
		}
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		return response.Code
	}
	for _, route := range routes {
		if got := perform(route.method, route.path, route.body, twoFactorSession, "", ""); got != http.StatusUnauthorized {
			t.Errorf("%s with 2FA and no code = %d, want 401", route.path, got)
		}
		if got := perform(route.method, route.path, route.body, twoFactorSession, "", "000000"); got != http.StatusUnauthorized {
			t.Errorf("%s with wrong code = %d, want 401", route.path, got)
		}
		code, err := totp.GenerateCode(key.Secret(), time.Now())
		if err != nil {
			t.Fatalf("generate code: %v", err)
		}
		if got := perform(route.method, route.path, route.body, twoFactorSession, "", code); got != http.StatusBadRequest {
			t.Errorf("%s with valid code = %d, want 400", route.path, got)
		}
		if got := perform(route.method, route.path, route.body, plainSession, "", ""); got != http.StatusBadRequest {
			t.Errorf("%s without 2FA = %d, want 400", route.path, got)
		}
		if got := perform(route.method, route.path, route.body, "", apiKey, ""); got != http.StatusBadRequest {
			t.Errorf("%s with API key = %d, want 400", route.path, got)
		}
	}
}
