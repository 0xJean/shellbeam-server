package jsonrpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/pquerna/otp/totp"
)

func editSettings(p *rpc.Principal, params map[string]any) *rpc.JsonRpcResponse {
	meta := &rpc.ContextMeta{Principal: p, Permission: p.PrimaryRole(), UserUUID: p.UserUUID}
	req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, ID: 1, Method: "admin:editSettings", Params: params}
	return dispatchWithSensitive(context.Background(), nil, meta, req)
}

func TestEditSettingsWritesAPIKeyAndChallengesCredentialChanges(t *testing.T) {
	twoFactor, secret := testUser(t, true)
	plain, _ := testUser(t, false)
	user := rpc.NewUserPrincipal(twoFactor)

	// The finding: without 2FA in the way, this call stores a working API key.
	key := uuid.NewString()
	if resp := editSettings(rpc.NewUserPrincipal(plain), map[string]any{config.ApiKeyKey: key}); resp.Error != nil {
		t.Fatalf("account without 2FA: %+v", resp.Error)
	}
	if stored, _ := config.GetAs[string](config.ApiKeyKey); stored != key {
		t.Fatalf("api_key = %q, want %q", stored, key)
	}

	for name, params := range map[string]map[string]any{
		"api_key":                {config.ApiKeyKey: "x" + key},
		"auto_discovery_key":     {config.AutoDiscoveryKeyKey: uuid.NewString()},
		"o_auth_enabled":         {config.OAuthEnabledKey: true},
		"o_auth_provider":        {config.OAuthProviderKey: "provider-" + uuid.NewString()},
		"disable_password_login": {config.DisablePasswordLoginKey: true},
		"with another key":       {config.ApiKeyKey: "x" + key, config.SitenameKey: "Mixed"},
	} {
		resp := editSettings(user, params)
		if resp.Error == nil || resp.Error.Code != rpc.PermissionDenied {
			t.Errorf("%s without code: %+v", name, resp.Error)
		}
	}
	if stored, _ := config.GetAs[string](config.ApiKeyKey); stored != key {
		t.Fatal("an unchallenged request changed api_key")
	}

	// Ordinary settings and an unchanged key, as in a whole-form save, pass.
	if resp := editSettings(user, map[string]any{config.SitenameKey: "Plain", config.ApiKeyKey: key}); resp.Error != nil {
		t.Fatalf("unchanged key: %+v", resp.Error)
	}

	// An API key principal stays exempt, which is how the client writes settings.
	if resp := editSettings(rpc.NewAPIKeyPrincipal(), map[string]any{config.ApiKeyKey: key}); resp.Error != nil {
		t.Fatalf("API key principal: %+v", resp.Error)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	rotated := "y" + key
	if resp := editSettings(user, map[string]any{config.ApiKeyKey: rotated, "2fa_code": code}); resp.Error != nil {
		t.Fatalf("with code: %+v", resp.Error)
	}
	if stored, _ := config.GetAs[string](config.ApiKeyKey); stored != rotated {
		t.Fatalf("api_key = %q, want %q", stored, rotated)
	}
	all, err := config.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	if _, stored := all["2fa_code"]; stored {
		t.Fatal("the challenge code was stored as a setting")
	}
}

func TestEditSettingsRefusesUnknownKeys(t *testing.T) {
	admin := rpc.NewAPIKeyPrincipal()
	for _, key := range []string{"made_up", "metric_migration_target", config.ThemeMarketSourcesKey, "internal_timestamp_utc_migrated"} {
		resp := editSettings(admin, map[string]any{key: "value-" + uuid.NewString(), config.SitenameKey: "Refused"})
		if resp.Error == nil || resp.Error.Code != rpc.InvalidParams || !strings.Contains(resp.Error.Message, key) {
			t.Errorf("%s: %+v", key, resp.Error)
		}
	}
	if name, _ := config.GetAs[string](config.SitenameKey, "unset"); name == "Refused" {
		t.Fatal("a refused update was partly applied")
	}

	// What the client and the acceptance scripts send still saves.
	if resp := editSettings(admin, map[string]any{
		"sitename": "Known", "description": "d", "private_site": false, "geo_ip_enabled": false,
		"notification_enabled": false, "notification_method": "webhook", "notification_template": "{{message}}",
		"login_notification": false, "expire_notification_enabled": false, "expire_notification_lead_days": 3,
		"traffic_limit_percentage": 80.5, "eula_accepted": true, "tempory_share_token": "t", "tempory_share_token_expire_at": 1,
	}); resp.Error != nil {
		t.Fatalf("known keys: %+v", resp.Error)
	}

	// Everything getSettings returns can be sent straight back.
	all, err := config.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	all["id"] = 1
	all["updated_at"] = "2026-01-01T00:00:00Z"
	if resp := editSettings(admin, all); resp.Error != nil {
		t.Fatalf("round trip: %+v", resp.Error)
	}
}
