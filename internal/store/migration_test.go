package store_test

import (
	"path/filepath"
	"testing"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

func TestMigrationExportImport(t *testing.T) {
	dir := t.TempDir()
	src, err := store.Open(filepath.Join(dir, "src.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := src.EnsureAdmin("admin", "oldpass"); err != nil {
		t.Fatal(err)
	}
	_ = src.SetSetting("panel_url", "https://old.example.com")
	_ = src.SetSetting("backbone_pass", "bb-secret")
	_ = src.SetSetting("cf_api_token", "cf-token")

	n := &model.Node{
		ID: "n_testnode01", Name: "cm7", Role: model.RoleRelay,
		PublicIP: "1.2.3.4", AgentToken: "tok_fixed_token_abc",
		ListenPort: 10401, PortMin: 10401, PortMax: 10499,
	}
	if err := src.CreateNode(n); err != nil {
		t.Fatal(err)
	}
	// CreateNode may keep our ID/token
	gotN, _ := src.GetNode("n_testnode01")
	if gotN == nil || gotN.AgentToken != "tok_fixed_token_abc" {
		t.Fatalf("node token lost: %#v", gotN)
	}

	r := &model.Route{Name: "main", Enabled: true, HopsJSON: `[{"node_id":"n_testnode01","order":0}]`}
	if err := src.CreateRoute(r); err != nil {
		t.Fatal(err)
	}
	rid := r.ID

	u := &model.User{
		Username: "alice", ProxyPassword: "proxypass1234", Status: model.StatusActive,
		TrafficLimitBytes: 10 << 30, SubToken: "subtokfixed001", RouteID: &rid,
		Note: "vip", DisplayMultiplier: 2,
	}
	if err := src.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	// force known id path: re-export after create gets auto id
	uid := u.ID
	if uid == 0 {
		t.Fatal("user id")
	}

	a := &model.Announcement{Title: "hi", Body: "body", Enabled: true, Popup: true}
	if err := src.CreateAnnouncement(a); err != nil {
		t.Fatal(err)
	}

	if err := src.AddTraffic(uid, "n_exit", 100, 200); err != nil {
		t.Fatal(err)
	}

	snap, err := src.ExportMigration("v-test")
	if err != nil {
		t.Fatal(err)
	}
	if !snap.SecretsIncluded || snap.Format != store.MigrationFormat {
		t.Fatalf("snap meta %#v", snap)
	}
	if len(snap.Nodes) != 1 || snap.Nodes[0].AgentToken != "tok_fixed_token_abc" {
		t.Fatalf("nodes %#v", snap.Nodes)
	}
	if len(snap.Users) != 1 || snap.Users[0].ProxyPassword != "proxypass1234" || snap.Users[0].SubToken != "subtokfixed001" {
		t.Fatalf("users %#v", snap.Users)
	}
	if snap.Settings["backbone_pass"] != "bb-secret" || snap.Settings["cf_api_token"] != "cf-token" {
		t.Fatalf("settings %#v", snap.Settings)
	}
	if len(snap.Admins) != 1 || snap.Admins[0].PasswordHash == "" {
		t.Fatalf("admins %#v", snap.Admins)
	}

	// bad format must fail validation (copy slices so we don't mutate snap)
	bad := *snap
	bad.Format = "nope"
	if err := store.ValidateMigrationSnapshot(&bad); err == nil {
		t.Fatal("expected format error")
	}
	badNodes := append([]store.MigrationNode(nil), snap.Nodes...)
	badNodes[0].AgentToken = ""
	bad2 := *snap
	bad2.Nodes = badNodes
	if err := store.ValidateMigrationSnapshot(&bad2); err == nil {
		t.Fatal("expected token error")
	}

	dst, err := store.Open(filepath.Join(dir, "dst.db"))
	if err != nil {
		t.Fatal(err)
	}
	// seed junk that must be wiped
	_ = dst.EnsureAdmin("junk", "junkpass")
	_ = dst.SetSetting("panel_url", "https://junk")
	junk := &model.Node{Name: "junk", Role: model.RoleExit, PublicIP: "9.9.9.9"}
	_ = dst.CreateNode(junk)

	if err := dst.ImportMigration(snap); err != nil {
		t.Fatal(err)
	}

	// admin password from old hash
	admin, err := dst.GetAdminByUsername("admin")
	if err != nil || admin == nil {
		t.Fatalf("admin: %v", err)
	}
	if !store.CheckPassword(admin.PasswordHash, "oldpass") {
		t.Fatal("admin password not restored")
	}
	if _, err := dst.GetAdminByUsername("junk"); err == nil {
		t.Fatal("junk admin should be gone")
	}

	v, _ := dst.GetSetting("panel_url")
	if v != "https://old.example.com" {
		t.Fatalf("panel_url %q", v)
	}
	v, _ = dst.GetSetting("backbone_pass")
	if v != "bb-secret" {
		t.Fatalf("backbone %q", v)
	}

	n2, err := dst.GetNode("n_testnode01")
	if err != nil || n2 == nil {
		t.Fatalf("node: %v", err)
	}
	if n2.AgentToken != "tok_fixed_token_abc" || n2.Name != "cm7" {
		t.Fatalf("node restore %#v", n2)
	}
	if _, err := dst.GetNode(junk.ID); err == nil {
		t.Fatal("junk node should be gone")
	}

	routes, _ := dst.ListRoutes()
	if len(routes) != 1 || routes[0].ID != rid || routes[0].Name != "main" {
		t.Fatalf("routes %#v", routes)
	}

	u2, err := dst.GetUser(uid)
	if err != nil || u2 == nil {
		t.Fatalf("user: %v", err)
	}
	if u2.Username != "alice" || u2.ProxyPassword != "proxypass1234" || u2.SubToken != "subtokfixed001" {
		t.Fatalf("user %#v", u2)
	}
	if u2.RouteID == nil || *u2.RouteID != rid {
		t.Fatalf("route bind %#v", u2.RouteID)
	}
	if u2.DisplayMultiplier != 2 {
		t.Fatalf("mult %v", u2.DisplayMultiplier)
	}

	anns, _ := dst.ListAnnouncements()
	if len(anns) != 1 || anns[0].Title != "hi" {
		t.Fatalf("anns %#v", anns)
	}

	// round-trip export again
	snap2, err := dst.ExportMigration("v-test2")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap2.Nodes) != 1 || snap2.Nodes[0].AgentToken != "tok_fixed_token_abc" {
		t.Fatalf("re-export nodes %#v", snap2.Nodes)
	}
	if len(snap2.Users) != 1 || snap2.Users[0].ID != uid {
		t.Fatalf("re-export users %#v", snap2.Users)
	}
}

func TestMigrationImportRejectsIncomplete(t *testing.T) {
	dir := t.TempDir()
	dst, err := store.Open(filepath.Join(dir, "d.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = dst.EnsureAdmin("admin", "x")
	_ = dst.SetSetting("keep", "1")

	err = dst.ImportMigration(&store.MigrationSnapshot{
		Format: store.MigrationFormat, FormatVersion: store.MigrationFormatVersion,
		SecretsIncluded: true,
		Admins:          []store.MigrationAdmin{{Username: "a", PasswordHash: "h"}},
		Nodes:           []store.MigrationNode{{ID: "n1", Name: "n", Role: "exit", AgentToken: ""}},
	})
	if err == nil {
		t.Fatal("expected reject")
	}
	// DB must not be half-wiped
	v, _ := dst.GetSetting("keep")
	if v != "1" {
		t.Fatalf("db polluted after failed import: %q", v)
	}
}
