package configgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRebuildHybridHasLocalMieru(t *testing.T) {
	st := openTestStore(t)
	// seed admin not needed for rebuild
	n := &model.Node{
		Name:     "cm7",
		Role:     model.RoleHybrid,
		PublicIP: "1.2.3.4",
		PrivateIP: "10.0.0.7",
		PortMin:  1080,
		PortMax:  1080,
	}
	if err := st.CreateNode(n); err != nil {
		t.Fatal(err)
	}
	// need ≥1 active user for socks/mita
	u := &model.User{
		Username:      "alice",
		ProxyPassword: "secret",
		Status:        model.StatusActive,
	}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	// route with hybrid only
	hops, _ := json.Marshal([]model.Hop{{NodeID: n.ID, Order: 0}})
	r := &model.Route{Name: "direct", Enabled: true, HopsJSON: string(hops)}
	if err := st.CreateRoute(r); err != nil {
		t.Fatal(err)
	}

	b := &Builder{Store: st}
	if err := b.RebuildAll(); err != nil {
		t.Fatal(err)
	}
	_, raw, err := st.GetDesiredConfig(n.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg model.AgentDesiredConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, p := range cfg.Plugins {
		typ, _ := p["type"].(string)
		types[typ] = true
		if typ == "mieru_client" {
			pc, _ := p["config"].(map[string]interface{})
			if pc["server"] != "127.0.0.1" {
				t.Fatalf("hybrid mieru should dial loopback, got %v", pc["server"])
			}
			if pc["link_user"] == "" || pc["link_user"] == nil {
				t.Fatal("missing backbone link_user on hybrid mieru")
			}
		}
		if typ == "socks_in" {
			pc, _ := p["config"].(map[string]interface{})
			if pc["upstream_host"] != "127.0.0.1" {
				t.Fatalf("hybrid socks should upstream local mieru, got %v", pc)
			}
		}
	}
	for _, need := range []string{"mita_server", "mieru_client", "socks_in"} {
		if !types[need] {
			t.Fatalf("hybrid missing plugin %s (have %v)", need, types)
		}
	}
	// backbone persisted
	bb, _ := st.GetSetting(SettingBackboneUser)
	if bb == "" {
		t.Fatal("backbone user not saved")
	}
	// backbone in mita users
	foundBB := false
	for _, u := range cfg.Users {
		if u.Username == bb {
			foundBB = true
		}
	}
	if !foundBB {
		t.Fatalf("backbone %s not in hybrid users", bb)
	}
}

func TestRebuildRelayToExit(t *testing.T) {
	st := openTestStore(t)
	relay := &model.Node{Name: "r1", Role: model.RoleRelay, PublicIP: "5.5.5.5", PrivateIP: "10.0.0.5", PortMin: 8964, PortMax: 8964}
	exit := &model.Node{Name: "e1", Role: model.RoleExit, PublicIP: "6.6.6.6", PrivateIP: "10.0.0.6", PortMin: 10001, PortMax: 10001}
	if err := st.CreateNode(relay); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateNode(exit); err != nil {
		t.Fatal(err)
	}
	u := &model.User{Username: "bob", ProxyPassword: "pw", Status: model.StatusActive}
	if err := st.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	hops, _ := json.Marshal([]model.Hop{
		{NodeID: relay.ID, Order: 0},
		{NodeID: exit.ID, Order: 1, CapabilityType: "mita_server"},
	})
	r := &model.Route{Name: "chain", Enabled: true, HopsJSON: string(hops)}
	if err := st.CreateRoute(r); err != nil {
		t.Fatal(err)
	}
	b := &Builder{Store: st}
	if err := b.RebuildAll(); err != nil {
		t.Fatal(err)
	}
	_, raw, err := st.GetDesiredConfig(relay.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cfg model.AgentDesiredConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	var mitaHost string
	var mitaPort float64
	for _, p := range cfg.Plugins {
		if p["type"] == "mieru_client" {
			pc, _ := p["config"].(map[string]interface{})
			mitaHost, _ = pc["server"].(string)
			mitaPort, _ = pc["port"].(float64)
		}
	}
	// Prefer private IP of exit
	if mitaHost != "10.0.0.6" {
		t.Fatalf("relay should dial exit private IP, got %q", mitaHost)
	}
	if int(mitaPort) != 10001 {
		t.Fatalf("relay should dial exit mita port 10001, got %v", mitaPort)
	}
	_ = os.Getenv // silence unused in some go versions
}
