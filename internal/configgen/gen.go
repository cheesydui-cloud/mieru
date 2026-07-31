package configgen

import (
	"encoding/json"
	"sort"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// Local control ports for mieru client on relay — never equal node public listen.
const (
	localMieruSocks = 19080
	localMieruRPC   = 18964
)

// Builder builds per-node desired configs from routes + users.
// Data plane:
//
//	Exit  → mita_server (real process) on EffectiveListenPort / explicit range
//	Relay → mieru_client (→ exit mita) + socks_in (public, upstream local mieru socks5)
//	Entry → socks_in (public, upstream next hop PublicServicePort)
//	Hybrid → mita on HybridMitaPort + socks_in on EffectiveListenPort
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

	// Backbone credentials: prefer a non-sticky active user so every exit accepts it.
	// Fall back to first active user (still inject onto every exit below).
	linkUser, linkPass := pickLinkUser(users)

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
			// Prefer single primary port when range collapses; multi-port only if operator set span.
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
			cfg.Users = exitUsers(users, n.ID, linkUser, linkPass)
			socksPort := n.PublicServicePort()
			mitaPort := n.MitaPrimaryPort()
			if mitaPort == socksPort {
				mitaPort = model.HybridMitaPort(socksPort)
			}
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mita_server",
				"config": map[string]interface{}{
					"listen_port": mitaPort,
					"port_min":    mitaPort,
					"port_max":    mitaPort,
				},
			})
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "socks_in",
				"config": map[string]interface{}{
					"auth":        "users",
					"listen_port": socksPort,
					"port_min":    socksPort,
					"port_max":    socksPort,
				},
			})

		case model.RoleEntry:
			for _, u := range users {
				cfg.Users = append(cfg.Users, model.AgentUser{
					UserID:   u.ID,
					Username: u.Username,
					Password: u.ProxyPassword,
					Enabled:  true,
				})
			}
			upHost, upPort := "", 0
			// find next hop from routes involving this entry
			for _, r := range routes {
				if !r.Enabled {
					continue
				}
				var hops []model.Hop
				_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
				involved := false
				for _, h := range hops {
					if h.NodeID == n.ID {
						involved = true
						break
					}
				}
				if !involved {
					continue
				}
				next := b.nextAgentHopAfter(hops, n.ID)
				if next == nil {
					continue
				}
				host := next.PublicIP
				if host == "" {
					host = next.Hostname
				}
				if host != "" {
					upHost = host
					upPort = next.PublicServicePort()
					break
				}
			}
			// fallback: any user route next hop
			if upHost == "" {
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
					next := b.nextAgentHopAfter(hops, n.ID)
					if next == nil && len(hops) > 0 {
						for _, h := range hops {
							if h.NodeID == "" || h.External {
								continue
							}
							if nn, err := b.Store.GetNode(h.NodeID); err == nil {
								next = nn
								break
							}
						}
					}
					if next == nil {
						continue
					}
					host := next.PublicIP
					if host == "" {
						host = next.Hostname
					}
					if host != "" {
						upHost = host
						upPort = next.PublicServicePort()
						break
					}
				}
			}

			emin, emax := n.EffectivePortRange()
			socksCfg := map[string]interface{}{
				"auth":        "users",
				"listen_port": n.PublicServicePort(),
				"port_min":    emin,
				"port_max":    emax,
			}
			if upHost != "" && upPort > 0 {
				socksCfg["upstream_host"] = upHost
				socksCfg["upstream_port"] = upPort
			}
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type":   "socks_in",
				"config": socksCfg,
			})

		case model.RoleRelay:
			for _, u := range users {
				cfg.Users = append(cfg.Users, model.AgentUser{
					UserID:   u.ID,
					Username: u.Username,
					Password: u.ProxyPassword,
					Enabled:  true,
					MitaUser: u.Username,
				})
			}

			// Prefer exits that appear on enabled routes with this relay; stable order by node id.
			exitList := b.exitsForRelay(routes, n.ID)
			pubPort := n.PublicServicePort()
			// Keep local mieru control ports off the public listen port.
			socksLocal := localMieruSocks
			rpcLocal := localMieruRPC
			if pubPort == socksLocal {
				socksLocal = 19081
			}
			if pubPort == rpcLocal {
				rpcLocal = 18965
			}

			if len(exitList) > 0 {
				ex := exitList[0]
				host := ex.PublicIP
				if host == "" {
					host = ex.Hostname
				}
				if host != "" {
					mcfg := map[string]interface{}{
						"server":      host,
						"port":        ex.MitaPrimaryPort(),
						"exit_id":     ex.ID,
						"socks5_port": socksLocal,
						"rpc_port":    rpcLocal,
					}
					if linkUser != "" {
						mcfg["link_user"] = linkUser
						mcfg["link_password"] = linkPass
					}
					cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
						"type":   "mieru_client",
						"config": mcfg,
					})
				}
			}

			pmin, pmax := n.EffectivePortRange()
			socksCfg := map[string]interface{}{
				"auth":        "users",
				"listen_port": pubPort,
				"port_min":    pmin,
				"port_max":    pmax,
			}
			if len(exitList) > 0 {
				socksCfg["upstream_host"] = "127.0.0.1"
				socksCfg["upstream_port"] = socksLocal
				socksCfg["via"] = "mieru_client"
				// Local mieru socks is typically no-auth; dialViaSocks5 already offers 0x00.
			}
			if routeHasExternalEntryTo(routes, n.ID) {
				socksCfg["via_external"] = true
			}
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type":   "socks_in",
				"config": socksCfg,
			})
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

// pickLinkUser chooses backbone credentials present on as many exits as possible.
func pickLinkUser(users []model.User) (string, string) {
	// 1) non-sticky first
	for _, u := range users {
		if u.Username != "" && u.ProxyPassword != "" && u.StickyExitID == "" {
			return u.Username, u.ProxyPassword
		}
	}
	// 2) any active
	for _, u := range users {
		if u.Username != "" && u.ProxyPassword != "" {
			return u.Username, u.ProxyPassword
		}
	}
	return "", ""
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
	// Ensure backbone credentials exist even if sticky filtered them out.
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
			// also accept any exit if no route mentions this relay (orphan relay fallback)
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
	}
	// Fallback: if no route mentions this relay, pick any exit (legacy behaviour, stable sort).
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
				if h.CapabilityType == "mita_server" || h.Order == len(hops)-1 {
					if ex, err := b.Store.GetNode(h.NodeID); err == nil && (ex.Role == model.RoleExit || ex.Role == model.RoleHybrid) {
						found[ex.ID] = *ex
					}
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
