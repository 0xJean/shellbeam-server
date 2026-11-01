package public

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/web/oauth"
	"github.com/komari-monitor/komari/web/oauth/factory"
	"github.com/pquerna/otp/totp"
)

type fakeOAuthProvider struct{ name string }

func (p *fakeOAuthProvider) GetName() string                         { return p.name }
func (p *fakeOAuthProvider) GetConfiguration() factory.Configuration { return &struct{}{} }
func (p *fakeOAuthProvider) GetAuthorizationURL(string) (string, string) {
	return "", ""
}
func (p *fakeOAuthProvider) OnCallback(*gin.Context, string, map[string]string, string) (factory.OidcCallback, error) {
	return factory.OidcCallback{UserId: "external-user"}, nil
}
func (p *fakeOAuthProvider) Init() error    { return nil }
func (p *fakeOAuthProvider) Destroy() error { return nil }

func loadFakeOAuthProvider(t *testing.T, name string) {
	t.Helper()
	factory.RegisterOidcProvider(func() factory.IOidcProvider { return &fakeOAuthProvider{name: name} })
	if err := oauth.LoadProvider(name, "{}"); err != nil {
		t.Fatalf("load provider: %v", err)
	}
	t.Cleanup(func() { _ = oauth.Shutdown() })
}

func oauthTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/api/oauth_callback", OAuthCallback)
	r.POST("/api/oauth_2fa", OAuthTwoFactor)
	return r
}

func createOAuthUser(t *testing.T, ssoID, totpSecret string) models.User {
	t.Helper()
	user, err := accounts.CreateAccount("oauth-"+uuid.NewString(), "Password123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := accounts.BindingExternalAccount(user.UUID, ssoID); err != nil {
		t.Fatalf("bind user: %v", err)
	}
	if totpSecret != "" {
		if err := accounts.Enable2Fa(user.UUID, totpSecret); err != nil {
			t.Fatalf("enable 2fa: %v", err)
		}
	}
	t.Cleanup(func() {
		dbcore.GetDBInstance().Where("uuid = ?", user.UUID).Delete(&models.Session{})
		dbcore.GetDBInstance().Where("uuid = ?", user.UUID).Delete(&models.User{})
	})
	return user
}

func oauthCallback(r http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func responseCookie(response *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name && cookie.MaxAge >= 0 {
			return cookie
		}
	}
	return nil
}

func TestOAuthStateValid(t *testing.T) {
	cases := []struct {
		name, provider string
		binding        bool
		cookie, query  string
		want           bool
	}{
		{"oidc match", "generic", false, "a", "a", true},
		{"oidc mismatch", "generic", false, "a", "b", false},
		{"missing cookie", "generic", false, "", "", false},
		{"qq login skips comparison", "qq", false, "a", "", true},
		{"qq login needs cookie", "qq", false, "", "", false},
		{"qq binding mismatch", "qq", true, "a", "", false},
		{"qq binding match", "qq", true, "a", "a", true},
		{"oidc binding mismatch", "generic", true, "a", "b", false},
	}
	for _, tc := range cases {
		if got := oauthStateValid(tc.provider, tc.binding, tc.cookie, tc.query); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestQQStateSkipCannotCompleteBinding(t *testing.T) {
	loadFakeOAuthProvider(t, "qq")
	victim := createOAuthUser(t, "qq_original", "")
	session, err := accounts.CreateSession(victim.UUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	response := oauthCallback(oauthTestRouter(), "/api/oauth_callback?code=x",
		&http.Cookie{Name: "oauth_state", Value: "cookie-state"},
		&http.Cookie{Name: "binding_external_account", Value: victim.UUID},
		&http.Cookie{Name: "session_token", Value: session},
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	stored, err := accounts.GetUserByUUID(victim.UUID)
	if err != nil || stored.SSOID != "qq_original" {
		t.Fatalf("binding changed: sso=%q err=%v", stored.SSOID, err)
	}
}

func TestOAuthLoginWithout2FACreatesSession(t *testing.T) {
	loadFakeOAuthProvider(t, "fakeplain")
	createOAuthUser(t, "fakeplain_external-user", "")
	response := oauthCallback(oauthTestRouter(), "/api/oauth_callback?state=s", &http.Cookie{Name: "oauth_state", Value: "s"})
	if response.Code != http.StatusFound || responseCookie(response, "session_token") == nil {
		t.Fatalf("status = %d, session cookie = %v", response.Code, responseCookie(response, "session_token"))
	}
}

func TestOAuthLoginRequiresTOTPWhen2FAEnabled(t *testing.T) {
	loadFakeOAuthProvider(t, "fake2fa")
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "test"})
	if err != nil {
		t.Fatalf("generate totp: %v", err)
	}
	createOAuthUser(t, "fake2fa_external-user", key.Secret())
	r := oauthTestRouter()

	callback := oauthCallback(r, "/api/oauth_callback?state=s", &http.Cookie{Name: "oauth_state", Value: "s"})
	if responseCookie(callback, "session_token") != nil {
		t.Fatal("callback issued a session before TOTP verification")
	}
	ticket := responseCookie(callback, oauthTicketCookie)
	if ticket == nil || !strings.Contains(callback.Body.String(), "2fa_code") {
		t.Fatalf("callback did not start the TOTP step: %d %s", callback.Code, callback.Body.String())
	}

	submit := func(code string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/oauth_2fa", strings.NewReader(url.Values{"2fa_code": {code}}.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(ticket)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		return response
	}

	wrong := submit("000000")
	if wrong.Code != http.StatusUnauthorized || responseCookie(wrong, "session_token") != nil {
		t.Fatalf("wrong code: status = %d", wrong.Code)
	}
	code, err := totp.GenerateCode(key.Secret(), time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	right := submit(code)
	if right.Code != http.StatusSeeOther || responseCookie(right, "session_token") == nil {
		t.Fatalf("right code: status = %d: %s", right.Code, right.Body.String())
	}
	if replay := submit(code); replay.Code != http.StatusUnauthorized || responseCookie(replay, "session_token") != nil {
		t.Fatalf("ticket replay: status = %d", replay.Code)
	}
}

func TestOAuthTicketAttemptsAreLimited(t *testing.T) {
	ticket := issueOAuthTicket("user")
	t.Cleanup(func() { deleteOAuthTicket(ticket) })
	for attempt := 1; attempt <= oauthTicketMaxAttempts; attempt++ {
		if _, ok := beginOAuthTicketAttempt(ticket); !ok {
			t.Fatalf("attempt %d rejected", attempt)
		}
	}
	if _, ok := beginOAuthTicketAttempt(ticket); ok {
		t.Fatal("attempt past the limit accepted")
	}
}
