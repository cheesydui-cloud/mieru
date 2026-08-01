package configgen

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// Local control ports for mieru client — never equal node public listen.
const (
	localMieruSocks = 19080
	localMieruRPC   = 18964
)

// Backbone settings keys (panel-managed tunnel credentials, not end-user accounts).
const (
	SettingBackboneUser = "backbone_user"
	SettingBackbonePass = "backbone_pass"
)

// Builder builds per-node desired configs from routes + users.
//
// Data plane (OneClick client protocol + multi-hop residential exit):
//
//	Exit    → mita_server (panel end-users + backbone). Real internet egress.
//	Relay   → tcp_forward(public port → exit mita). Transparent pipe so phone can
//	          speak official mierus:// to the front IP while auth/egress stay on US mita.
//	Entry   → same as relay when next hop is exit/hybrid (tcp_forward to mita);
//	          socks_in only as legacy chain when next is another socks hop.
//	Hybrid  → mita + local mieru + public socks (single-box OneClick shape; egress = this node).
//
// Client path for "国内前置 + 美国家宽落地" (e.g. TK):
//
//	phone ──mierus──► front(relay):10401 ──raw TCP──► US mita:8964 ──► residential IP
//
// Share links advertise the front host/port; passwords are validated on the exit mita.
type Builder struct {
	Store *store.Store
}

func (b *Builder) RebuildAll() error {
	nodes, err := b.Store.ListNodes()
	if err != nil {
		return err
	}
	users, err := b.Store.ListActiveProxyUsers()
	if err != nil {
		return err
	}
	routes, err := b.Store.ListRoutes()
	if err != nil {
		return err
	}
	routeByID := map[int64]model.Route{}
	for _, r := range routes {
		routeByID[r.ID] = r
	}

	linkUser, linkPass, err := b.ensureBackbone()
	if err != nil {
		return fmt.Errorf("backbone: %w", err)
	}

	// Persist expanded front port pools so list/edit UI matches EffectivePortRange
	// (legacy single-port front: 10401–10401 → 10401–10499).
	for i := range nodes {
		n := &nodes[i]
		if n.Role != model.RoleRelay && n.Role != model.RoleEntry {
			continue
		}
		pmin, pmax := n.EffectivePortRange()
		if n.PortMin != pmin || n.PortMax != pmax {
			n.PortMin = pmin
			n.PortMax = pmax
			if n.ListenPort <= 0 {
				n.ListenPort = pmin
			}
			_ = b.Store.UpdateNode(n)
		}
	}

	for _, n := range nodes {
		cfg := model.AgentDesiredConfig{
			NodeID:  n.ID,
			Role:    n.Role,
			Plugins: []map[string]interface{}{},
			Users:   []model.AgentUser{},
			ACL:     map[string]interface{}{},
		}

		switch n.Role {
		case model.RoleExit:
			cfg.Users = exitUsers(users, n.ID, linkUser, linkPass, routes, routeByID)
			pmin, pmax := n.EffectivePortRange()
			listen := n.MitaPrimaryPort()
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mita_server",
				"config": map[string]interface{}{
					"listen_port": listen,
					"port_min":    pmin,
					"port_max":    pmax,
				},
			})

		case model.RoleHybrid:
			// OneClick single-node shape on the hybrid box:
			//   mita (exit) ← mieru client (loopback) ← public socks_in
			// Relays elsewhere still dial mita on MitaPrimaryPort with backbone.
			cfg.Users = exitUsers(users, n.ID, linkUser, linkPass, routes, routeByID)
			socksPort := n.PublicServicePort()
			mitaPort := n.MitaPrimaryPort()
			if mitaPort == socksPort {
				mitaPort = model.HybridMitaPort(socksPort)
			}
			socksLocal, rpcLocal := localPorts(socksPort)

			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mita_server",
				"config": map[string]interface{}{
					"listen_port": mitaPort,
					"port_min":    mitaPort,
					"port_max":    mitaPort,
				},
			})
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mieru_client",
				"config": map[string]interface{}{
					"server":        "127.0.0.1",
					"port":          mitaPort,
					"exit_id":       n.ID,
					"socks5_port":   socksLocal,
					"rpc_port":      rpcLocal,
					"link_user":     linkUser,
					"link_password": linkPass,
					// OneClick defaults
					"multiplexing":   "MULTIPLEXING_OFF",
					"handshake_mode": "HANDSHAKE_NO_WAIT",
					"mtu":            1400,
				},
			})
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "socks_in",
				"config": map[string]interface{}{
					"auth":          "users",
					"listen_port":   socksPort,
					"port_min":      socksPort,
					"port_max":      socksPort,
					"upstream_host": "127.0.0.1",
					"upstream_port": socksLocal,
					"via":           "mieru_client",
				},
			})

		case model.RoleEntry:
			// Entry is the domestic front: transparent TCP to exit mita so clients
			// can use official mierus:// while egress stays on residential exit.
			// Multi-exit: one listen port per enabled route (not the whole pool).
			// No tunnel → empty plugins; agent stops any previous tcp_forward/socks_in.
			forwards := b.frontForwardsForNode(n, routes)
			if len(forwards) > 0 {
				cfg.Plugins = append(cfg.Plugins, tcpForwardPlugin(forwards))
			}

		case model.RoleRelay:
			// Domestic front / mid hop: transparent TCP pipe to exit mita.
			// Client speaks mieru end-to-end; this node does not terminate mieru.
			// Multi-exit: one listen port per enabled route sharing this front.
			// No tunnel → empty plugins (do NOT fall back to socks_in — that kept
			// ports open after operators deleted all tunnels).
			forwards := b.frontForwardsForNode(n, routes)
			if len(forwards) > 0 {
				cfg.Plugins = append(cfg.Plugins, tcpForwardPlugin(forwards))
			}
		}

		ver, err := b.Store.BumpNodeConfigVersion(n.ID)
		if err != nil {
			return err
		}
		cfg.Version = ver
		raw, _ := json.Marshal(cfg)
		if err := b.Store.SaveDesiredConfig(n.ID, ver, string(raw)); err != nil {
			return err
		}
	}
	return nil
}

// ensureBackbone returns stable tunnel credentials, creating them once if missing.
func (b *Builder) ensureBackbone() (user, pass string, err error) {
	user, _ = b.Store.GetSetting(SettingBackboneUser)
	pass, _ = b.Store.GetSetting(SettingBackbonePass)
	user = strings.TrimSpace(user)
	pass = strings.TrimSpace(pass)
	if user != "" && pass != "" {
		return user, pass, nil
	}
	// Generate once — do not rotate on every rebuild (would desync live agents mid-apply).
	user = "bb_" + randomHex(6)
	pass = randomHex(16)
	if err := b.Store.SetSetting(SettingBackboneUser, user); err != nil {
		return "", "", err
	}
	if err := b.Store.SetSetting(SettingBackbonePass, pass); err != nil {
		return "", "", err
	}
	return user, pass, nil
}

func randomHex(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		// extremely unlikely; still return something non-empty
		return fmt.Sprintf("x%dx", nBytes)
	}
	return hex.EncodeToString(b)
}

func localPorts(pubPort int) (socksLocal, rpcLocal int) {
	socksLocal = localMieruSocks
	rpcLocal = localMieruRPC
	if pubPort == socksLocal {
		socksLocal = 19081
	}
	if pubPort == rpcLocal {
		rpcLocal = 18965
	}
	if rpcLocal == socksLocal {
		rpcLocal = socksLocal + 1
	}
	return socksLocal, rpcLocal
}

// resolveNextHop finds the node after `nodeID` on any enabled route involving it.
// Prefer routes that actually list this node; fall back to first hop of user-bound routes.
func (b *Builder) resolveNextHop(nodeID string, routes []model.Route, users []model.User, routeByID map[int64]model.Route) *model.Node {
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
		involved := false
		for _, h := range hops {
			if h.NodeID == nodeID {
				involved = true
				break
			}
		}
		if !involved {
			continue
		}
		if next := b.nextAgentHopAfter(hops, nodeID); next != nil {
			return next
		}
	}
	// Fallback: user-bound routes
	for _, u := range users {
		if u.RouteID == nil {
			continue
		}
		r, ok := routeByID[*u.RouteID]
		if !ok || !r.Enabled {
			continue
		}
		var hops []model.Hop
		_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
		next := b.nextAgentHopAfter(hops, nodeID)
		if next == nil && len(hops) > 0 {
			// node may be implicit first hop — take first agent hop
			for _, h := range hops {
				if h.NodeID == "" || h.External {
					continue
				}
				if nn, err := b.Store.GetNode(h.NodeID); err == nil {
					// if this entry is not in hops, still use first real hop as upstream
					if nodeID != nn.ID {
						return nn
					}
				}
			}
		}
		if next != nil {
			return next
		}
	}
	return nil
}

// frontForward is one public listen on a front (entry/relay) → exit mita.
type frontForward struct {
	RouteID    int64
	ListenPort int
	ExitID     string
	TargetHost string
	TargetPort int
	Comment    string
}

// frontForwardsForNode builds one tcp_forward rule per enabled route that uses
// this front node and has an exit/hybrid after it. Ports:
//
//  1. hop.Port on the front hop (operator override)
//  2. else sequential from node PortMin pool (10401, 10402, …)
//  3. single-route / no pool → PublicServicePort (backward compatible)
//
// Same exit on multiple routes still gets distinct ports when routes differ,
// so merchant DNAT can map each public port independently.
func (b *Builder) frontForwardsForNode(n model.Node, routes []model.Route) []frontForward {
	type cand struct {
		routeID int64
		hopPort int
		exit    *model.Node
		name    string
	}
	var cands []cand
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		if err := json.Unmarshal([]byte(r.HopsJSON), &hops); err != nil {
			continue
		}
		idx := -1
		var hopPort int
		for i, h := range hops {
			if h.NodeID == n.ID {
				idx = i
				hopPort = h.Port
				break
			}
		}
		if idx < 0 {
			continue
		}
		var exit *model.Node
		for j := idx + 1; j < len(hops); j++ {
			h := hops[j]
			if h.NodeID == "" || h.External {
				continue
			}
			nn, err := b.Store.GetNode(h.NodeID)
			if err != nil {
				continue
			}
			if nn.Role == model.RoleExit || nn.Role == model.RoleHybrid {
				exit = nn
				break
			}
		}
		if exit == nil {
			continue
		}
		cands = append(cands, cand{routeID: r.ID, hopPort: hopPort, exit: exit, name: r.Name})
	}
	if len(cands) == 0 {
		return nil
	}
	// Stable order: route id ascending so port assignment is deterministic.
	sort.Slice(cands, func(i, j int) bool { return cands[i].routeID < cands[j].routeID })

	pmin, pmax := n.EffectivePortRange()
	base := n.PublicServicePort()
	used := map[int]bool{}
	nextAuto := pmin
	if nextAuto <= 0 {
		nextAuto = base
	}

	alloc := func(prefer int) int {
		if prefer > 0 && !used[prefer] {
			used[prefer] = true
			return prefer
		}
		// Prefer sequential from pool; stay within [pmin,pmax] when multi-port pool.
		for p := nextAuto; ; p++ {
			if pmax > pmin && p > pmax {
				// Pool exhausted — keep going above max (operator must open DNAT).
				// Still unique among our rules.
			}
			if p < 1 {
				p = 1
			}
			if used[p] {
				continue
			}
			// Skip reserved SSH-ish only if pool starts higher; otherwise allow.
			used[p] = true
			nextAuto = p + 1
			return p
		}
	}

	// First pass: honor explicit hop.Port so operator can pin.
	// When only one candidate and no hop port and pool is single-port → base.
	out := make([]frontForward, 0, len(cands))
	for _, c := range cands {
		prefer := c.hopPort
		if prefer <= 0 && len(cands) == 1 && (pmax <= pmin || pmax-pmin == 0) {
			prefer = base
		}
		// Multi-route without hop port: allocate from pool starting at pmin.
		listen := alloc(prefer)
		host := c.exit.DialHost()
		mitaPort := c.exit.MitaPrimaryPort()
		if host == "" || mitaPort <= 0 {
			continue
		}
		comment := fmt.Sprintf("route#%d %s → %s mita", c.routeID, c.name, c.exit.Name)
		out = append(out, frontForward{
			RouteID:    c.routeID,
			ListenPort: listen,
			ExitID:     c.exit.ID,
			TargetHost: host,
			TargetPort: mitaPort,
			Comment:    comment,
		})
	}
	return out
}

// tcpForwardPlugin emits one tcp_forward plugin with rules[] (multi-target).
func tcpForwardPlugin(forwards []frontForward) map[string]interface{} {
	rules := make([]map[string]interface{}, 0, len(forwards))
	for _, f := range forwards {
		rules = append(rules, map[string]interface{}{
			"listen_port": f.ListenPort,
			"target_host": f.TargetHost,
			"target_port": f.TargetPort,
			"exit_id":     f.ExitID,
			"route_id":    f.RouteID,
			"comment":     f.Comment,
		})
	}
	// Primary listen_port = first rule (diagnostics / single-port UIs).
	primary := forwards[0].ListenPort
	return map[string]interface{}{
		"type": "tcp_forward",
		"config": map[string]interface{}{
			"listen_port": primary,
			"port_min":    primary,
			"port_max":    primary,
			"rules":       rules,
			"comment":     "client mierus → exit mita (per-route ports)",
		},
	}
}

// FrontListenPort returns the public listen port on frontNodeID for the given
// enabled route (same allocation as configgen). Used by share/subscription.
// Returns 0 if the front is not on the route or no exit follows it.
func FrontListenPort(st *store.Store, frontNodeID string, route *model.Route) int {
	if st == nil || frontNodeID == "" || route == nil || !route.Enabled {
		return 0
	}
	n, err := st.GetNode(frontNodeID)
	if err != nil {
		return 0
	}
	routes, err := st.ListRoutes()
	if err != nil {
		return 0
	}
	b := &Builder{Store: st}
	forwards := b.frontForwardsForNode(*n, routes)
	for _, f := range forwards {
		if f.RouteID == route.ID {
			return f.ListenPort
		}
	}
	return 0
}

// resolveExitTarget finds the exit/hybrid this front node should TCP-forward to.
// Prefers an exit that appears after nodeID on an enabled route; falls back to
// exitsForRelay (any co-routed exit). Kept for diagnostics / single-target callers.
func (b *Builder) resolveExitTarget(nodeID string, routes []model.Route) *model.Node {
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
		idx := -1
		for i, h := range hops {
			if h.NodeID == nodeID {
				idx = i
				break
			}
		}
		if idx < 0 {
			continue
		}
		for j := idx + 1; j < len(hops); j++ {
			h := hops[j]
			if h.NodeID == "" || h.External {
				continue
			}
			n, err := b.Store.GetNode(h.NodeID)
			if err != nil {
				continue
			}
			if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
				return n
			}
		}
	}
	list := b.exitsForRelay(routes, nodeID)
	if len(list) > 0 {
		n := list[0]
		return &n
	}
	return nil
}

// exitUsers builds mita user list for one exit node.
//
// Only users whose bound tunnel (route) includes this exit get accounts here.
// Users with no route_id are omitted (must bind a tunnel first).
// StickyExitID still filters when set. Backbone link user is always included
// so mieru_client on hybrid can authenticate.
func exitUsers(users []model.User, exitID, linkUser, linkPass string, routes []model.Route, routeByID map[int64]model.Route) []model.AgentUser {
	out := make([]model.AgentUser, 0, len(users)+1)
	seen := map[string]bool{}
	// exitID → set of route IDs that hop through this exit
	routesOnExit := map[int64]bool{}
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		if err := json.Unmarshal([]byte(r.HopsJSON), &hops); err != nil {
			continue
		}
		for _, h := range hops {
			if h.NodeID == exitID {
				routesOnExit[r.ID] = true
				break
			}
		}
	}
	for _, u := range users {
		if u.StickyExitID != "" && u.StickyExitID != exitID {
			continue
		}
		// Must be bound to a tunnel that uses this exit — otherwise deleting
		// tunnels would leave ghost accounts on every exit.
		if u.RouteID == nil || *u.RouteID <= 0 {
			continue
		}
		if !routesOnExit[*u.RouteID] {
			// route may have been deleted; skip
			if _, ok := routeByID[*u.RouteID]; ok && !routesOnExit[*u.RouteID] {
				continue
			}
			continue
		}
		out = append(out, model.AgentUser{
			UserID:   u.ID,
			Username: u.Username,
			Password: u.ProxyPassword,
			Enabled:  u.Status == model.StatusActive,
			MitaUser: u.Username,
		})
		seen[u.Username] = true
	}
	// Backbone always present so mieru_client on hybrid can authenticate
	// even when no end-user is sticky to this exit.
	if linkUser != "" && linkPass != "" && !seen[linkUser] {
		out = append(out, model.AgentUser{
			UserID:   0,
			Username: linkUser,
			Password: linkPass,
			Enabled:  true,
			MitaUser: linkUser,
		})
	}
	return out
}

// exitsForRelay returns exit/hybrid nodes that share an enabled route with this relay.
func (b *Builder) exitsForRelay(routes []model.Route, relayID string) []model.Node {
	found := map[string]model.Node{}
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
		hasRelay := false
		for _, h := range hops {
			if h.NodeID == relayID {
				hasRelay = true
				break
			}
		}
		if !hasRelay {
			continue
		}
		for _, h := range hops {
			if h.NodeID == "" {
				continue
			}
			if h.CapabilityType == "mita_server" || h.Order == len(hops)-1 {
				if ex, err := b.Store.GetNode(h.NodeID); err == nil && (ex.Role == model.RoleExit || ex.Role == model.RoleHybrid) {
					found[ex.ID] = *ex
				}
			}
		}
		// Also: any exit/hybrid after the relay in hop order
		for i, h := range hops {
			if h.NodeID != relayID {
				continue
			}
			for j := i + 1; j < len(hops); j++ {
				if hops[j].NodeID == "" || hops[j].External {
					continue
				}
				if ex, err := b.Store.GetNode(hops[j].NodeID); err == nil && (ex.Role == model.RoleExit || ex.Role == model.RoleHybrid) {
					found[ex.ID] = *ex
				}
			}
		}
	}
	// Fallback: any exit on any enabled route (orphan relay).
	if len(found) == 0 {
		for _, r := range routes {
			if !r.Enabled {
				continue
			}
			var hops []model.Hop
			_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
			for _, h := range hops {
				if h.NodeID == "" {
					continue
				}
				if ex, err := b.Store.GetNode(h.NodeID); err == nil && (ex.Role == model.RoleExit || ex.Role == model.RoleHybrid) {
					found[ex.ID] = *ex
				}
			}
		}
	}
	ids := make([]string, 0, len(found))
	for id := range found {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]model.Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, found[id])
	}
	return out
}

func (b *Builder) nextAgentHopAfter(hops []model.Hop, nodeID string) *model.Node {
	return nextAgentHopAfterStore(b.Store, hops, nodeID)
}

func nextAgentHopAfterStore(st *store.Store, hops []model.Hop, nodeID string) *model.Node {
	if st == nil || len(hops) == 0 {
		return nil
	}
	start := -1
	if nodeID == "" {
		for i, h := range hops {
			if h.External || h.NodeID == "" {
				start = i
				break
			}
		}
	} else {
		for i, h := range hops {
			if h.NodeID == nodeID {
				start = i
				break
			}
		}
		if start < 0 {
			return nil
		}
	}
	for j := start + 1; j < len(hops); j++ {
		h := hops[j]
		if h.External || h.NodeID == "" {
			continue
		}
		n, err := st.GetNode(h.NodeID)
		if err != nil {
			continue
		}
		return n
	}
	return nil
}

func routeHasExternalEntryTo(routes []model.Route, nodeID string) bool {
	for _, r := range routes {
		if !r.Enabled {
			continue
		}
		var hops []model.Hop
		if err := json.Unmarshal([]byte(r.HopsJSON), &hops); err != nil {
			continue
		}
		for i, h := range hops {
			if !h.External && h.NodeID != "" {
				continue
			}
			for j := i + 1; j < len(hops); j++ {
				if hops[j].External || hops[j].NodeID == "" {
					continue
				}
				if hops[j].NodeID == nodeID {
					return true
				}
				break
			}
		}
	}
	return false
}
