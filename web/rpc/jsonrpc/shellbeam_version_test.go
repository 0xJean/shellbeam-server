package jsonrpc

import (
	"encoding/json"
	"testing"

	"github.com/komari-monitor/komari/pkg/rpc"
	"github.com/komari-monitor/komari/utils"
)

func TestVersionHandlersPreserveClientAndPluginCompatibility(t *testing.T) {
	for name, handler := range map[string]rpc.Handler{
		"legacy REST and RPC": getVersion,
		"public RPC":          publicGetVersion,
	} {
		t.Run(name, func(t *testing.T) {
			result, rpcErr := handler(t.Context(), nil)
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			// Match legacy clients: extra metadata must not replace the original
			// fields or put the ShellBeam release number in the API version.
			var legacy struct {
				Version string `json:"version"`
				Hash    string `json:"hash"`
			}
			if err := json.Unmarshal(data, &legacy); err != nil {
				t.Fatal(err)
			}
			if legacy.Version != "1.6.0" || legacy.Hash == "" {
				t.Fatalf("incompatible legacy version response: %s", data)
			}
			var fork struct {
				Product        string `json:"product"`
				ProductVersion string `json:"product_version"`
				UpstreamCommit string `json:"upstream_commit"`
			}
			if err := json.Unmarshal(data, &fork); err != nil {
				t.Fatal(err)
			}
			if fork.Product != "ShellBeam Server" || fork.ProductVersion != utils.ServerVersion || fork.UpstreamCommit == "" {
				t.Fatalf("missing ShellBeam release identity: %s", data)
			}
		})
	}
}
