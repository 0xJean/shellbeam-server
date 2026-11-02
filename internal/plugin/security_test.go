package plugin

import (
	"net/http"
	"testing"

	"github.com/komari-monitor/komari/database/models"
)

func installManifest(t *testing.T, short, permissions string) {
	t.Helper()
	zipPath := writePluginZip(t, map[string]string{
		"komari-plugin.json": `{"name":"P","short":"` + short + `","version":"1.0.0","permissions":` + permissions + `,"pages":[{"file":"p.html","title":"P","visibility":"public"}]}`,
		"script.js":          `function load() {}`,
		"p.html":             `<script>1</script>`,
	})
	if _, err := InstallZip(zipPath); err != nil {
		t.Fatal(err)
	}
}

func TestPluginPageHeaders(t *testing.T) {
	withTempDataDir(t)
	csp := func(short string) string {
		header := http.Header{}
		SetPageSecurityHeaders(header, short)
		if header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff missing", short)
		}
		return header.Get("Content-Security-Policy")
	}
	const sandbox = "sandbox allow-scripts"

	installManifest(t, "plain", `{}`)
	if got := csp("plain"); got != sandbox {
		t.Errorf("plugin without approval: %q", got)
	}
	installManifest(t, "execonly", `{"allowExec":true}`)
	st := global.stateStore().get("execonly")
	st.ApprovedPermissionsHash = approvalPermissionsHash(models.PluginPermissions{AllowExec: true})
	global.stateStore().set("execonly", st)
	if got := csp("execonly"); got != sandbox {
		t.Errorf("approved permission without same-origin need: %q", got)
	}
	installManifest(t, "routes", `{"allowRoutes":true}`)
	if got := csp("routes"); got != sandbox {
		t.Errorf("unapproved routes: %q", got)
	}
	st = global.stateStore().get("routes")
	st.ApprovedPermissionsHash = approvalPermissionsHash(models.PluginPermissions{AllowRoutes: true})
	global.stateStore().set("routes", st)
	if got := csp("routes"); got != "" {
		t.Errorf("approved routes: %q", got)
	}
	if got := csp("missing"); got != sandbox {
		t.Errorf("missing plugin: %q", got)
	}
}

func TestNodePermissionNeedsApproval(t *testing.T) {
	withTempDataDir(t)
	installManifest(t, "nodeonly", `{"node":true,"timeout":5}`)
	if err := SetEnabled("nodeonly", true, false); err != ErrPermissionApprovalRequired {
		t.Fatalf("enable without approval: %v", err)
	}
	if err := SetEnabled("nodeonly", true, true); err != nil {
		t.Fatalf("enable with approval: %v", err)
	}
	if err := SetEnabled("nodeonly", false, false); err != nil {
		t.Fatal(err)
	}
	if approvalPermissionsHash(models.PluginPermissions{AllowRoutes: true}) ==
		approvalPermissionsHash(models.PluginPermissions{AllowRoutes: true, Node: true}) {
		t.Fatal("node must change the approval hash")
	}
}

func TestApprovalHashMigration(t *testing.T) {
	withTempDataDir(t)
	node := models.PluginPermissions{Node: true, AllowRoutes: true, AllowSystemRPC: true}
	const permissions = `{"node":true,"allowRoutes":true,"allowSystemRPC":true,"timeout":5}`

	// The bundled extension's shape, approved before node joined the hash.
	installManifest(t, "kept", permissions)
	global.stateStore().set("kept", PluginState{Enabled: true, ApprovedPermissionsHash: hashApprovalPermissions(node, false)})
	// Enabled with no approval at all: stays out.
	installManifest(t, "never", `{"node":true,"timeout":5}`)
	global.stateStore().set("never", PluginState{Enabled: true})

	_ = LoadAll()
	if inst := global.instanceFor("kept"); inst == nil {
		t.Fatalf("approved plugin was not kept: %+v", global.stateStore().get("kept"))
	}
	st := global.stateStore().get("kept")
	if st.ApprovedPermissionsHash != approvalPermissionsHash(node) || st.ApprovalHashSchema != approvalHashSchema {
		t.Fatalf("state not migrated: %+v", st)
	}
	if err := SetEnabled("kept", false, false); err != nil {
		t.Fatal(err)
	}
	if global.instanceFor("never") != nil || global.stateStore().get("never").Enabled {
		t.Fatal("never-approved node plugin stayed enabled")
	}

	// Once stamped, an old-format hash is no longer accepted: this is an upgrade
	// that added node to an approved plugin.
	installManifest(t, "added", permissions)
	global.stateStore().set("added", PluginState{ApprovedPermissionsHash: hashApprovalPermissions(node, false), ApprovalHashSchema: approvalHashSchema})
	global.migrateApprovalHash("added")
	if err := SetEnabled("added", true, false); err != ErrPermissionApprovalRequired {
		t.Fatalf("stamped old hash: %v", err)
	}
}
