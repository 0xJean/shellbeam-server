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
