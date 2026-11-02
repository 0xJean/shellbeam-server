package router

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/web/api"
	"github.com/pquerna/otp/totp"
)

func twoFactorRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(api.IdentityMiddleware())
	Register(r)
	return r
}

func testSecret(t *testing.T) string {
	t.Helper()
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "test"})
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	return key.Secret()
}

func currentCode(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	return code
}

func TestEnable2FAChallengesOnlyAReplacement(t *testing.T) {
	r := twoFactorRouter()
	user, err := accounts.CreateAccount("enroll-"+uuid.NewString(), "Password123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	enable := func(newSecret, challenge string) int {
		query := url.Values{"code": {currentCode(t, newSecret)}}
		if challenge != "" {
			query.Set("2fa_code", challenge)
		}
		request := httptest.NewRequest(http.MethodPost, "/api/admin/2fa/enable?"+query.Encode(), nil)
		request.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		request.AddCookie(&http.Cookie{Name: "2fa_secret", Value: newSecret})
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		return response.Code
	}
	stored := func() string {
		current, err := accounts.GetUserByUUID(user.UUID)
		if err != nil {
			t.Fatalf("read user: %v", err)
		}
		return current.TwoFactor
	}

	first, replacement := testSecret(t), testSecret(t)
	if got := enable(first, ""); got != http.StatusOK {
		t.Fatalf("first enrolment = %d, want 200", got)
	}
	if stored() != first {
		t.Fatal("first enrolment did not store the secret")
	}
	if got := enable(replacement, ""); got != http.StatusUnauthorized {
		t.Fatalf("replacement without challenge = %d, want 401", got)
	}
	// A code from the new secret must not answer the challenge for the old one.
	if got := enable(replacement, currentCode(t, replacement)); got != http.StatusUnauthorized {
		t.Fatalf("replacement challenged with the new secret = %d, want 401", got)
	}
	if stored() != first {
		t.Fatal("an unchallenged request replaced the secret")
	}
	if got := enable(replacement, currentCode(t, first)); got != http.StatusOK {
		t.Fatalf("replacement with challenge = %d, want 200", got)
	}
	if stored() != replacement {
		t.Fatal("challenged replacement did not store the secret")
	}
}

func TestDownloadBackupChallengeReachesGETByHeaderAndQuery(t *testing.T) {
	t.Chdir(t.TempDir())
	r := twoFactorRouter()
	secret := testSecret(t)
	user, err := accounts.CreateAccount("backup-"+uuid.NewString(), "Password123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := accounts.Enable2Fa(user.UUID, secret); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}
	session, err := accounts.CreateSession(user.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	get := func(query, header string) int {
		request := httptest.NewRequest(http.MethodGet, "/api/admin/download/backup"+query, nil)
		request.AddCookie(&http.Cookie{Name: "session_token", Value: session})
		if header != "" {
			request.Header.Set("X-2FA-Code", header)
		}
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		return response.Code
	}
	if got := get("", ""); got != http.StatusUnauthorized {
		t.Fatalf("no code = %d, want 401", got)
	}
	if got := get("?2fa_code=000000", ""); got != http.StatusUnauthorized {
		t.Fatalf("wrong code = %d, want 401", got)
	}
	if got := get("", currentCode(t, secret)); got == http.StatusUnauthorized {
		t.Fatal("header code was rejected")
	}
	if got := get("?2fa_code="+currentCode(t, secret), ""); got == http.StatusUnauthorized {
		t.Fatal("query code was rejected")
	}
}
