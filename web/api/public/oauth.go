package public

import (
	"crypto/rand"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/auditlog"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/utils"
	"github.com/komari-monitor/komari/web/oauth"
)

const (
	oauthTicketCookie      = "oauth_2fa_ticket"
	oauthTicketTTL         = 5 * time.Minute
	oauthTicketMaxAttempts = 5
)

// oauthTicket records an OAuth login that passed the provider and still needs a
// TOTP code. It holds no session; one is created only after the code verifies.
type oauthTicket struct {
	userUUID string
	expires  time.Time
	attempts int
}

var (
	oauthTicketsMu sync.Mutex
	oauthTickets   = map[string]*oauthTicket{}
)

const oauthTwoFactorPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>Two-factor authentication</title></head>
<body><form method="post" action="/api/oauth_2fa">
<label>Two-factor authentication code <input name="2fa_code" inputmode="numeric" autocomplete="one-time-code" autofocus required></label>
<button type="submit">Continue</button>
</form></body></html>`

// oauthStateValid checks the callback state. The QQ aggregator callback does not
// return our state, so plain QQ login only proves the cookie exists and the
// provider then checks it against its own cache. That weaker check must never
// complete an account binding, which requires an exact match for every provider.
func oauthStateValid(provider string, binding bool, cookieState, queryState string) bool {
	if cookieState == "" {
		return false
	}
	providersSkipStateCheck := []string{"qq"}
	if !binding && slices.Contains(providersSkipStateCheck, provider) {
		return true
	}
	return cookieState == queryState
}

// /api/oauth
func OAuth(c *gin.Context) {
	OAuthEnabled, _ := config.GetAs[bool](config.OAuthEnabledKey, false)
	if !OAuthEnabled {
		c.JSON(403, gin.H{"status": "error", "error": "OAuth is not enabled"})
		return
	}

	authURL, state := oauth.CurrentProvider().GetAuthorizationURL(utils.GetCallbackURL(c))

	c.SetCookie("oauth_state", state, 3600, "/", "", false, true)

	c.Redirect(302, authURL)
}

// /api/oauth_callback
func OAuthCallback(c *gin.Context) {

	// 验证state防止CSRF攻击
	state, _ := c.Cookie("oauth_state")
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	// 如果cookie中有binding_external_account，说明是绑定外部账号
	// 否则是登录
	uuid, _ := c.Cookie("binding_external_account")
	c.SetCookie("binding_external_account", "", -1, "/", "", false, true)

	// 获取当前OAuth提供商名称
	providerName := oauth.CurrentProvider().GetName()

	if !oauthStateValid(providerName, uuid != "", state, c.Query("state")) {
		c.JSON(400, gin.H{"status": "error", "error": "Invalid state"})
		return
	}

	queries := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			queries[key] = values[0]
		}
	}
	oidcUser, err := oauth.CurrentProvider().OnCallback(c, state, queries, utils.GetCallbackURL(c))
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "error": "Failed to get user info: " + err.Error()})
		return
	}

	// ID作为SSO ID
	sso_id := fmt.Sprintf("%s_%s", oauth.CurrentProvider().GetName(), oidcUser.UserId)

	if uuid != "" {
		// 绑定外部账号
		session, _ := c.Cookie("session_token")
		user, err := accounts.GetUserBySession(session)
		if err != nil || user.UUID != uuid {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		err = accounts.BindingExternalAccount(user.UUID, sso_id)
		if err != nil {
			c.JSON(500, gin.H{"status": "error", "message": "Binding failed"})
			return
		}
		auditlog.Log(c.ClientIP(), user.UUID, "bound external account (OAuth)"+fmt.Sprintf(",sso_id: %s", sso_id), "login")
		c.Redirect(302, "/admin/dashboard")
		return
	}

	// 尝试获取用户
	user, err := accounts.GetUserBySSO(sso_id)
	if err != nil {
		c.JSON(401, gin.H{
			"status":  "error",
			"message": "please log in and bind your external account first.",
		})
		return
	}

	// 开启了2FA时，与密码登录一样必须先通过TOTP校验
	if user.TwoFactor != "" {
		setOAuthTicketCookie(c, issueOAuthTicket(user.UUID), int(oauthTicketTTL.Seconds()))
		renderOAuthTwoFactorPage(c, http.StatusOK)
		return
	}

	completeOAuthLogin(c, user.UUID, http.StatusFound)
}

// /api/oauth_2fa
func OAuthTwoFactor(c *gin.Context) {
	ticketID, _ := c.Cookie(oauthTicketCookie)
	userUUID, ok := beginOAuthTicketAttempt(ticketID)
	if !ok {
		setOAuthTicketCookie(c, "", -1)
		c.JSON(401, gin.H{"status": "error", "message": "2FA login expired, please sign in again"})
		return
	}
	if valid, err := accounts.Verify2Fa(userUUID, c.PostForm("2fa_code")); err != nil || !valid {
		renderOAuthTwoFactorPage(c, http.StatusUnauthorized)
		return
	}
	deleteOAuthTicket(ticketID)
	setOAuthTicketCookie(c, "", -1)
	completeOAuthLogin(c, userUUID, http.StatusSeeOther)
}

func completeOAuthLogin(c *gin.Context, userUUID string, redirectStatus int) {
	// 创建会话
	session, err := accounts.CreateSession(userUUID, sessionCookieMaxAge, c.Request.UserAgent(), c.ClientIP(), "oauth")
	if err != nil {
		c.JSON(500, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// 设置cookie并返回
	setSessionCookie(c, session, sessionCookieMaxAge)
	auditlog.Log(c.ClientIP(), userUUID, "logged in (OAuth)", "login")
	c.Redirect(redirectStatus, "/admin/dashboard")
}

func renderOAuthTwoFactorPage(c *gin.Context, status int) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(status, "text/html; charset=utf-8", []byte(oauthTwoFactorPage))
}

func setOAuthTicketCookie(c *gin.Context, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     oauthTicketCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   utils.GetScheme(c) == "https",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func issueOAuthTicket(userUUID string) string {
	ticketID := rand.Text()
	now := time.Now()
	oauthTicketsMu.Lock()
	defer oauthTicketsMu.Unlock()
	for id, ticket := range oauthTickets {
		if now.After(ticket.expires) {
			delete(oauthTickets, id)
		}
	}
	oauthTickets[ticketID] = &oauthTicket{userUUID: userUUID, expires: now.Add(oauthTicketTTL)}
	return ticketID
}

// beginOAuthTicketAttempt consumes one attempt so a ticket cannot be used to
// guess TOTP codes without limit.
func beginOAuthTicketAttempt(ticketID string) (string, bool) {
	oauthTicketsMu.Lock()
	defer oauthTicketsMu.Unlock()
	ticket, ok := oauthTickets[ticketID]
	if !ok {
		return "", false
	}
	ticket.attempts++
	if time.Now().After(ticket.expires) || ticket.attempts > oauthTicketMaxAttempts {
		delete(oauthTickets, ticketID)
		return "", false
	}
	return ticket.userUUID, true
}

func deleteOAuthTicket(ticketID string) {
	oauthTicketsMu.Lock()
	defer oauthTicketsMu.Unlock()
	delete(oauthTickets, ticketID)
}
