package jsonrpc

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/komari-monitor/komari/internal/config"
	"github.com/komari-monitor/komari/pkg/rpc"
)

// challengedSettingKeys either hand out a credential that skips 2FA (an API key is
// exempt from every challenge) or change who can sign in.
var challengedSettingKeys = []string{
	config.ApiKeyKey,
	config.AutoDiscoveryKeyKey,
	config.OAuthEnabledKey,
	config.OAuthProviderKey,
	config.DisablePasswordLoginKey,
}

// settingsRequestOnlyKeys travel with a request but are not settings: challenge
// codes, and fields a client gets back from getSettings.
var settingsRequestOnlyKeys = []string{
	"2fa_code", "two_factor_code", "otp", "id", "created_at", "updated_at", "UpdatedAt",
}

var writableSettingKeys = sync.OnceValue(func() map[string]bool {
	keys := map[string]bool{
		// The share-link token has no other writer; clients set it here.
		"tempory_share_token":           true,
		"tempory_share_token_expire_at": true,
	}
	settings := reflect.TypeOf(config.Settings{})
	for i := 0; i < settings.NumField(); i++ {
		name, _, _ := strings.Cut(settings.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			keys[name] = true
		}
	}
	for key := range metricStoreConfigKeys {
		keys[key] = true
	}
	return keys
})

// settingUnchanged compares a submitted value with the stored one, so a client that
// saves a whole form with the current API key in it needs no challenge. A missing
// row equals an empty value.
func settingUnchanged(current map[string]any, key string, value any) bool {
	stored, ok := current[key]
	if !ok {
		return value == nil || value == "" || value == false
	}
	storedJSON, err := json.Marshal(stored)
	if err != nil {
		return false
	}
	valueJSON, err := json.Marshal(value)
	return err == nil && string(storedJSON) == string(valueJSON)
}

// settingsUpdateNeedsChallenge fails closed: anything it cannot compare is challenged.
func settingsUpdateNeedsChallenge(req *rpc.JsonRpcRequest) bool {
	cfg := make(map[string]any)
	if err := req.BindParams(&cfg); err != nil {
		return true
	}
	var current map[string]any
	for _, key := range challengedSettingKeys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if current == nil {
			var err error
			if current, err = config.GetAll(); err != nil {
				return true
			}
		}
		if !settingUnchanged(current, key, value) {
			return true
		}
	}
	return false
}

// normalizeSettingsUpdate removes what must not be stored and refuses unknown keys,
// which would otherwise reach internal rows such as metric_migration_target or
// theme_market_sources. An unknown key sent back unchanged is dropped, because
// getSettings returns every stored row.
func normalizeSettingsUpdate(cfg map[string]any) error {
	for _, key := range settingsRequestOnlyKeys {
		delete(cfg, key)
	}
	var unknown []string
	var current map[string]any
	for key, value := range cfg {
		if writableSettingKeys()[key] {
			continue
		}
		if current == nil {
			var err error
			if current, err = config.GetAll(); err != nil {
				return fmt.Errorf("read settings: %w", err)
			}
		}
		if _, stored := current[key]; stored && settingUnchanged(current, key, value) {
			delete(cfg, key)
			continue
		}
		unknown = append(unknown, key)
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("unknown settings: %s", strings.Join(unknown, ", "))
	}
	return nil
}
