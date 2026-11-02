package jsonrpc

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

func TestWebSocketStopsAfterSessionRevoked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userUUID, _ := testUser(t, false)
	session, err := accounts.CreateSession(userUUID, 3600, "test", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	r := gin.New()
	r.GET("/api/rpc2", OnRpcRequest)
	server := httptest.NewServer(r)
	defer server.Close()

	header := http.Header{"Cookie": {"session_token=" + session}, "Origin": {server.URL}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	request := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "admin:listClients"}
	var response rpc.JsonRpcResponse
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if response.Error != nil {
		t.Fatalf("call before revocation failed: %+v", response.Error)
	}

	if err := accounts.DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if err := conn.WriteJSON(request); err != nil {
		t.Fatalf("write: %v", err)
	}
	response = rpc.JsonRpcResponse{}
	if err := conn.ReadJSON(&response); err != nil {
		t.Fatalf("read: %v", err)
	}
	if response.Error == nil || response.Error.Code != rpc.PermissionDenied {
		t.Fatalf("call after revocation: %+v", response)
	}
	if err := conn.ReadJSON(&response); err == nil {
		t.Fatal("connection stayed open after revocation")
	}
}

func TestWebSocketRechecksTempShareOnEachMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	if err := config.Set(config.PrivateSiteKey, true); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = config.Set(config.PrivateSiteKey, false) }()
	if err := config.Set("tempory_share_token", "share-key"); err != nil {
		t.Fatal(err)
	}
	if err := config.Set("tempory_share_token_expire_at", time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.GET("/api/rpc2", OnRpcRequest)
	server := httptest.NewServer(r)
	defer server.Close()

	header := http.Header{"Cookie": {"temp_key=share-key"}, "Origin": {server.URL}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/rpc2", header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	call := func() *rpc.JsonRpcError {
		t.Helper()
		var response rpc.JsonRpcResponse
		if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "public:getNodesInformation"}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := conn.ReadJSON(&response); err != nil {
			t.Fatalf("read: %v", err)
		}
		return response.Error
	}
	if rpcErr := call(); rpcErr != nil && rpcErr.Code == rpc.PermissionDenied {
		t.Fatalf("valid share denied: %+v", rpcErr)
	}
	if err := config.Set("tempory_share_token_expire_at", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	if rpcErr := call(); rpcErr == nil || rpcErr.Code != rpc.PermissionDenied {
		t.Fatalf("expired share on an open socket: %+v", rpcErr)
	}
}
