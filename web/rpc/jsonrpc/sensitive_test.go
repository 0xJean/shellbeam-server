package jsonrpc

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/pquerna/otp/totp"
)

func testUser(t *testing.T, twoFactor bool) (userUUID, secret string) {
	t.Helper()
	user, err := accounts.CreateAccount("rpc-"+uuid.NewString(), "Password123")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if twoFactor {
		key, err := totp.Generate(totp.GenerateOpts{Issuer: "test", AccountName: "test"})
		if err != nil {
			t.Fatalf("generate totp: %v", err)
		}
		secret = key.Secret()
		if err := accounts.Enable2Fa(user.UUID, secret); err != nil {
			t.Fatalf("enable 2fa: %v", err)
		}
	}
	return user.UUID, secret
}

func TestSensitiveMethodsAreMarked(t *testing.T) {
	for _, method := range []string{
		"admin:exec", "admin:getAgentStartupConfig", "admin:dbExec", "admin:getClientToken",
		"admin:fileList", "admin:fileListRoots", "admin:fileStat", "admin:fileMkdir", "admin:fileDelete",
		"admin:fileMove", "admin:fileCopy", "admin:fileChmod", "admin:fileChown", "admin:fileSearch",
	} {
		if !rpc.IsSensitive(method) {
			t.Errorf("%s must be sensitive", method)
		}
	}
	if rpc.IsSensitive("admin:listClients") {
		t.Error("admin:listClients must not be sensitive")
	}
}

func TestSensitiveMethodChallengeByPrincipal(t *testing.T) {
	const method = "admin:getClientToken"
	call := func(p *rpc.Principal, params map[string]any) *rpc.JsonRpcResponse {
		meta := &rpc.ContextMeta{Principal: p, Permission: p.PrimaryRole(), UserUUID: p.UserUUID}
		req := &rpc.JsonRpcRequest{Version: rpc.RPC_VERSION, ID: 1, Method: method, Params: params}
		return dispatchWithSensitive(context.Background(), nil, meta, req)
	}
	denied := func(resp *rpc.JsonRpcResponse) bool {
		return resp.Error != nil && resp.Error.Code == rpc.PermissionDenied
	}

	withTwoFactor, secret := testUser(t, true)
	if resp := call(rpc.NewUserPrincipal(withTwoFactor), map[string]any{}); !denied(resp) {
		t.Fatalf("2FA account without code: %+v", resp.Error)
	}
	if resp := call(rpc.NewUserPrincipal(withTwoFactor), map[string]any{"2fa_code": "000000"}); !denied(resp) {
		t.Fatalf("2FA account with wrong code: %+v", resp.Error)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if resp := call(rpc.NewUserPrincipal(withTwoFactor), map[string]any{"2fa_code": code}); denied(resp) {
		t.Fatalf("2FA account with valid code denied: %+v", resp.Error)
	}

	withoutTwoFactor, _ := testUser(t, false)
	if resp := call(rpc.NewUserPrincipal(withoutTwoFactor), map[string]any{}); denied(resp) {
		t.Fatalf("account without 2FA denied: %+v", resp.Error)
	}
	if resp := call(rpc.NewAPIKeyPrincipal(), map[string]any{}); denied(resp) {
		t.Fatalf("API key denied: %+v", resp.Error)
	}
}
