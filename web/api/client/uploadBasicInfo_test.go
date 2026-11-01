package client

import (
	"net"
	"testing"
	"time"

	"github.com/komari-monitor/komari/cmd/flags"
	"github.com/komari-monitor/komari/database/dbcore"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/internal/config"
	v2 "github.com/komari-monitor/komari/protocol/v2"
	"github.com/komari-monitor/komari/utils/geoip"
)

type staticGeoIPProvider struct {
	name string
	iso  string
}

func (p staticGeoIPProvider) Name() string {
	return p.name
}

func (p staticGeoIPProvider) GetGeoInfo(ip net.IP) (*geoip.GeoInfo, error) {
	return &geoip.GeoInfo{ISOCode: p.iso, Name: p.iso}, nil
}

func (p staticGeoIPProvider) UpdateDatabase() error {
	return nil
}

func (p staticGeoIPProvider) Close() error {
	return nil
}

func TestV2BasicInfoFillsRegionFromGeoIP(t *testing.T) {
	flags.DatabaseType = "sqlite"
	flags.DatabaseFile = "file:v2_basic_info_geoip?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	if err := config.Set(config.GeoIpEnabledKey, true); err != nil {
		t.Fatalf("enable geoip: %v", err)
	}

	oldProvider := geoip.CurrentProvider
	geoip.CurrentProvider = staticGeoIPProvider{name: t.Name(), iso: "SG"}
	t.Cleanup(func() {
		geoip.CurrentProvider = oldProvider
	})

	clientUUID := "client-v2-geoip"
	now := time.Now().UTC()
	if err := db.Create(&models.Client{
		UUID:      clientUUID,
		Token:     "token-v2-geoip",
		Name:      "client_v2_geoip",
		CreatedAt: now,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}

	resp := handleV2RPC(clientUUID, v2.Request{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentBasicInfo,
		Params: map[string]interface{}{
			"info": map[string]interface{}{
				"ipv4": "8.8.8.8",
			},
		},
		ID: "basic-info",
	}, false)
	if resp.Error != nil {
		t.Fatalf("v2 basic info failed: %+v", resp.Error)
	}

	var got models.Client
	if err := db.First(&got, "uuid = ?", clientUUID).Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	want := geoip.GetRegionUnicodeEmoji("SG")
	if got.Region != want {
		t.Fatalf("expected GeoIP region to be saved, got %q", got.Region)
	}
}

func TestV2BasicInfoCannotSetAdministratorColumns(t *testing.T) {
	flags.DatabaseType = "sqlite"
	flags.DatabaseFile = "file:v2_basic_info_geoip?mode=memory&cache=shared"

	db := dbcore.GetDBInstance()
	clientUUID := "client-v2-allowlist"
	otherUUID := "client-v2-allowlist-other"
	now := time.Now().UTC()
	for _, client := range []models.Client{
		{UUID: clientUUID, Token: "token-v2-allowlist", Name: "original", Group: "original-group", CreatedAt: now, UpdatedAt: now},
		{UUID: otherUUID, Token: "token-v2-allowlist-other", Name: "other", CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(&client).Error; err != nil {
			t.Fatalf("create client: %v", err)
		}
	}

	// The payload an Agent really sends, plus columns it must not control.
	resp := handleV2RPC(clientUUID, v2.Request{
		JSONRPC: v2.Version,
		Method:  v2.MethodAgentBasicInfo,
		Params: map[string]interface{}{
			"info": map[string]interface{}{
				"cpu_name": "CPU", "cpu_cores": 4, "cpu_physical_cores": 2, "arch": "amd64",
				"os": "Linux", "kernel_version": "6.1", "ipv4": "192.0.2.1", "ipv6": "2001:db8::1",
				"mem_total": 1024, "swap_total": 512, "disk_total": 2048, "gpu_name": "GPU",
				"virtualization": "kvm", "version": "1.2.3",

				"uuid": otherUUID, "token": "stolen", "name": "renamed", "group": "attacker",
				"hidden": true, "remark": "attacker", "price": 1, "created_at": "2000-01-01T00:00:00Z",
			},
		},
		ID: "basic-info",
	}, false)
	if resp.Error != nil {
		t.Fatalf("v2 basic info failed: %+v", resp.Error)
	}

	var got models.Client
	if err := db.First(&got, "uuid = ?", clientUUID).Error; err != nil {
		t.Fatalf("load client: %v", err)
	}
	if got.CpuName != "CPU" || got.CpuCores != 4 || got.CpuPhysicalCores != 2 || got.Arch != "amd64" ||
		got.OS != "Linux" || got.KernelVersion != "6.1" || got.IPv4 != "192.0.2.1" || got.IPv6 != "2001:db8::1" ||
		got.MemTotal != 1024 || got.SwapTotal != 512 || got.DiskTotal != 2048 || got.GpuName != "GPU" ||
		got.Virtualization != "kvm" || got.Version != "1.2.3" {
		t.Fatalf("reported telemetry was not saved: %+v", got)
	}
	if got.Token != "token-v2-allowlist" || got.Name != "original" || got.Group != "original-group" ||
		got.Hidden || got.Remark != "" || got.Price != 0 || got.CreatedAt.Year() == 2000 {
		t.Fatalf("report changed administrator columns: %+v", got)
	}
	var other models.Client
	if err := db.First(&other, "uuid = ?", otherUUID).Error; err != nil {
		t.Fatalf("load other client: %v", err)
	}
	if other.CpuName != "" {
		t.Fatalf("report reached another client: %+v", other)
	}
}
