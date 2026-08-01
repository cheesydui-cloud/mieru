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
			cfg.Users = exitUsers(users, n.ID, linkUser, linkPass)
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
			cfg.Users = exitUsers(users, n.ID, linkUser, linkPass)
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
			next := b.resolveNextHop(n.ID, routes, users, routeByID)
			exitTarget := b.resolveExitTarget(n.ID, routes)
			emin, emax := n.EffectivePortRange()
			pubPort := n.PublicServicePort()

			if exitTarget != nil {
				host := exitTarget.DialHost()
				mitaPort := exitTarget.MitaPrimaryPort()
				if host != "" && mitaPort > 0 {
					// Single public port only — wide port pools (10401-10499) are
					// operator metadata, not 99 parallel listeners.
					cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
						"type": "tcp_forward",
						"config": map[string]interface{}{
							"listen_port": pubPort,
							"port_min":    pubPort,
							"port_max":    pubPort,
							"target_host": host,
							"target_port": mitaPort,
							"exit_id":     exitTarget.ID,
							"comment":     "client mierus → exit mita (transparent)",
						},
					})
				}
			}
			// Legacy: no exit resolved — chain SOCKS to next hop if any.
			if len(cfg.Plugins) == 0 {
				for _, u := range users {
					cfg.Users = append(cfg.Users, model.AgentUser{
						UserID: u.ID, Username: u.Username, Password: u.ProxyPassword, Enabled: true,
					})
				}
				socksCfg := map[string]interface{}{
					"auth":        "users",
					"listen_port": pubPort,
					"port_min":    emin,
					"port_max":    emax,
				}
				if next != nil {
					host := next.DialHost()
					upPort := next.PublicServicePort()
					if host != "" && upPort > 0 {
						socksCfg["upstream_host"] = host
						socksCfg["upstream_port"] = upPort
						socksCfg["next_role"] = next.Role
						socksCfg["next_id"] = next.ID
					}
				}
				cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
					"type":   "socks_in",
					"config": socksCfg,
				})
			}

		case model.RoleRelay:
			// Domestic front / mid hop: transparent TCP pipe to exit mita.
			// Client speaks mieru end-to-end; this node does not terminate mieru.
			exitList := b.exitsForRelay(routes, n.ID)
			pubPort := n.PublicServicePort()
			pmin, pmax := n.EffectivePortRange()

			if len(exitList) > 0 {
				ex := exitList[0]
				host := ex.DialHost()
				mitaPort := ex.MitaPrimaryPort()
				if host != "" && mitaPort > 0 {
					// Single public port (front entry). Do not open pmin..pmax.
					cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
						"type": "tcp_forward",
						"config": map[string]interface{}{
							"listen_port": pubPort,
							"port_min":    pubPort,
							"port_max":    pubPort,
							"target_host": host,
							"target_port": mitaPort,
							"exit_id":     ex.ID,
							"comment":     "client mierus → exit mita (transparent)",
						},
					})
				}
			}
			// Fallback if no exit on route: keep old socks shell so node is not silent.
			if len(cfg.Plugins) == 0 {
				for _, u := range users {
					cfg.Users = append(cfg.Users, model.AgentUser{
						UserID: u.ID, Username: u.Username, Password: u.ProxyPassword, Enabled: true, MitaUser: u.Username,
					})
				}
				cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
					"type": "socks_in",
					"config": map[string]interface{}{
						"auth":        "users",
						"listen_port": pubPort,
						"port_min":    pmin,
						"port_max":    pmax,
					},
				})
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

// resolveExitTarget finds the exit/hybrid this front node should TCP-forward to.
// Prefers an exit that appears after nodeID on an enabled route; falls back to
// exitsForRelay (any co-routed exit).
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

// exitUsers builds mita user list: sticky filter + always include backbone link user.
func exitUsers(users []model.User, exitID, linkUser, linkPass string) []model.AgentUser {
	out := make([]model.AgentUser, 0, len(users)+1)
	seen := map[string]bool{}
	for _, u := range users {
		if u.StickyExitID != "" && u.StickyExitID != exitID {
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
	// Backbone always present so mieru_client on relays/hybrid can authenticate
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
	// Refuse empty — mita plugin will fail closed; surface via empty list.
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
