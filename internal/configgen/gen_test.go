package configgen

import (
	"encoding/json"
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
	var targetHost string
	var targetPort float64
	var typ string
	var listenPort float64
	for _, p := range cfg.Plugins {
		if p["type"] == "tcp_forward" {
			typ = "tcp_forward"
			pc, _ := p["config"].(map[string]interface{})
			// Multi-rule form: rules[0]
			if rules, ok := pc["rules"].([]interface{}); ok && len(rules) > 0 {
				m, _ := rules[0].(map[string]interface{})
				targetHost, _ = m["target_host"].(string)
				targetPort, _ = m["target_port"].(float64)
				listenPort, _ = m["listen_port"].(float64)
			} else {
				targetHost, _ = pc["target_host"].(string)
				targetPort, _ = pc["target_port"].(float64)
				listenPort, _ = pc["listen_port"].(float64)
			}
		}
	}
	if typ != "tcp_forward" {
		t.Fatalf("relay should use tcp_forward to exit mita, plugins=%v", cfg.Plugins)
	}
	// Prefer private IP of exit (DialHost)
	if targetHost != "10.0.0.6" {
		t.Fatalf("relay should forward to exit private IP, got %q", targetHost)
	}
	if int(targetPort) != 10001 {
		t.Fatalf("relay should forward to exit mita port 10001, got %v", targetPort)
	}
	if int(listenPort) != 8964 {
		t.Fatalf("single-route relay should listen on PublicServicePort 8964, got %v", listenPort)
	}
}

func TestRebuildMultiExitDistinctPorts(t *testing.T) {
	st := openTestStore(t)
	// cm7-style front with port pool 10401-10499
	relay := &model.Node{
		Name: "cm7", Role: model.RoleRelay,
		PublicIP: "211.1.1.1", PrivateIP: "172.16.2.104",
		PortMin: 10401, PortMax: 10499,
	}
	exitA := &model.Node{Name: "us-a", Role: model.RoleExit, PublicIP: "1.1.1.1", PrivateIP: "10.0.0.10", PortMin: 8964, PortMax: 8964}
	exitB := &model.Node{Name: "us-b", Role: model.RoleExit, PublicIP: "2.2.2.2", PrivateIP: "10.0.0.20", PortMin: 8964, PortMax: 8964}
	for _, n := range []*model.Node{relay, exitA, exitB} {
		if err := st.CreateNode(n); err != nil {
			t.Fatal(err)
		}
	}
	hopsA, _ := json.Marshal([]model.Hop{
		{NodeID: relay.ID, Order: 0},
		{NodeID: exitA.ID, Order: 1, CapabilityType: "mita_server"},
	})
	hopsB, _ := json.Marshal([]model.Hop{
		{NodeID: relay.ID, Order: 0},
		{NodeID: exitB.ID, Order: 1, CapabilityType: "mita_server"},
	})
	rA := &model.Route{Name: "to-a", Enabled: true, HopsJSON: string(hopsA)}
	rB := &model.Route{Name: "to-b", Enabled: true, HopsJSON: string(hopsB)}
	if err := st.CreateRoute(rA); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRoute(rB); err != nil {
		t.Fatal(err)
	}
	// users bound to each route
	uA := &model.User{Username: "ua", ProxyPassword: "p", Status: model.StatusActive, RouteID: &rA.ID}
	uB := &model.User{Username: "ub", ProxyPassword: "p", Status: model.StatusActive, RouteID: &rB.ID}
	if err := st.CreateUser(uA); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser(uB); err != nil {
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
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	var rules []interface{}
	for _, p := range cfg.Plugins {
		if p["type"] != "tcp_forward" {
			continue
		}
		pc, _ := p["config"].(map[string]interface{})
		rules, _ = pc["rules"].([]interface{})
	}
	if len(rules) != 2 {
		t.Fatalf("want 2 tcp_forward rules for 2 exits, got %d: %v", len(rules), rules)
	}
	byExit := map[string]map[string]interface{}{}
	ports := map[int]bool{}
	for _, item := range rules {
		m, _ := item.(map[string]interface{})
		eid, _ := m["exit_id"].(string)
		byExit[eid] = m
		lp := int(m["listen_port"].(float64))
		if ports[lp] {
			t.Fatalf("duplicate listen port %d", lp)
		}
		ports[lp] = true
	}
	if byExit[exitA.ID] == nil || byExit[exitB.ID] == nil {
		t.Fatalf("missing exit in rules: %v", byExit)
	}
	if byExit[exitA.ID]["target_host"] != "10.0.0.10" {
		t.Fatalf("exitA target host: %v", byExit[exitA.ID]["target_host"])
	}
	if byExit[exitB.ID]["target_host"] != "10.0.0.20" {
		t.Fatalf("exitB target host: %v", byExit[exitB.ID]["target_host"])
	}
	// ports should be from pool 10401, 10402 (route id order)
	pA := int(byExit[exitA.ID]["listen_port"].(float64))
	pB := int(byExit[exitB.ID]["listen_port"].(float64))
	if pA < 10401 || pA > 10499 || pB < 10401 || pB > 10499 {
		t.Fatalf("ports out of pool: a=%d b=%d", pA, pB)
	}
	if pA == pB {
		t.Fatal("both routes share same front port")
	}
	// FrontListenPort must match configgen for share links
	if got := FrontListenPort(st, relay.ID, rA); got != pA {
		t.Fatalf("FrontListenPort routeA=%d want %d", got, pA)
	}
	if got := FrontListenPort(st, relay.ID, rB); got != pB {
		t.Fatalf("FrontListenPort routeB=%d want %d", got, pB)
	}
}

func TestRebuildMultiExitHopPortOverride(t *testing.T) {
	st := openTestStore(t)
	relay := &model.Node{
		Name: "cm7", Role: model.RoleRelay,
		PublicIP: "211.1.1.1", PrivateIP: "172.16.2.104",
		PortMin: 10401, PortMax: 10499,
	}
	exitA := &model.Node{Name: "us-a", Role: model.RoleExit, PublicIP: "1.1.1.1", PrivateIP: "10.0.0.10", PortMin: 8964, PortMax: 8964}
	exitB := &model.Node{Name: "us-b", Role: model.RoleExit, PublicIP: "2.2.2.2", PrivateIP: "10.0.0.20", PortMin: 8964, PortMax: 8964}
	for _, n := range []*model.Node{relay, exitA, exitB} {
		if err := st.CreateNode(n); err != nil {
			t.Fatal(err)
		}
	}
	hopsA, _ := json.Marshal([]model.Hop{
		{NodeID: relay.ID, Order: 0, Port: 10450},
		{NodeID: exitA.ID, Order: 1, CapabilityType: "mita_server"},
	})
	hopsB, _ := json.Marshal([]model.Hop{
		{NodeID: relay.ID, Order: 0, Port: 10460},
		{NodeID: exitB.ID, Order: 1, CapabilityType: "mita_server"},
	})
	rA := &model.Route{Name: "to-a", Enabled: true, HopsJSON: string(hopsA)}
	rB := &model.Route{Name: "to-b", Enabled: true, HopsJSON: string(hopsB)}
	if err := st.CreateRoute(rA); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateRoute(rB); err != nil {
		t.Fatal(err)
	}
	b := &Builder{Store: st}
	if err := b.RebuildAll(); err != nil {
		t.Fatal(err)
	}
	_, raw, _ := st.GetDesiredConfig(relay.ID)
	var cfg model.AgentDesiredConfig
	_ = json.Unmarshal([]byte(raw), &cfg)
	got := map[string]int{}
	for _, p := range cfg.Plugins {
		if p["type"] != "tcp_forward" {
			continue
		}
		pc, _ := p["config"].(map[string]interface{})
		for _, item := range pc["rules"].([]interface{}) {
			m := item.(map[string]interface{})
			got[m["exit_id"].(string)] = int(m["listen_port"].(float64))
		}
	}
	if got[exitA.ID] != 10450 || got[exitB.ID] != 10460 {
		t.Fatalf("hop port override failed: %v", got)
	}
}
