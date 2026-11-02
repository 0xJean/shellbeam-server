package jsonrpc

import (
	"context"
	"testing"

	"github.com/komari-monitor/komari/database/clients"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// Tokens come only from admin:getClientToken, the method that asks for 2FA.
func TestClientReadsDoNotReturnTokens(t *testing.T) {
	uuid, token, err := clients.CreateClientWithName("token-probe")
	if err != nil {
		t.Fatal(err)
	}
	if token == "" {
		t.Fatal("client was created without a token")
	}

	listed, rpcErr := adminListClients(context.Background(), &rpc.JsonRpcRequest{})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	found := false
	for _, client := range listed.([]models.Client) {
		if client.Token != "" {
			t.Errorf("listClients returned the token of %s", client.UUID)
		}
		found = found || client.UUID == uuid
	}
	if !found {
		t.Fatal("listClients did not return the probe client")
	}

	single, rpcErr := adminGetClient(context.Background(), &rpc.JsonRpcRequest{
		Version: rpc.RPC_VERSION, Params: map[string]any{"uuid": uuid},
	})
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if single.(models.Client).Token != "" {
		t.Error("getClient returned the token")
	}

	stored, err := clients.GetClientTokenByUUID(uuid)
	if err != nil || stored != token {
		t.Fatalf("stored token changed: %q, %v", stored, err)
	}
}
