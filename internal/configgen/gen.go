package configgen

import (
	"encoding/json"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// Builder builds per-node desired configs from routes + users.
// Data plane:
//
//	Exit  → mita_server (real process)
//	Relay → mieru_client (→ exit) + socks_in (public, upstream local mieru socks5)
//	Entry → socks_in (public, upstream next hop public socks)
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

	// backbone credentials for Relay mieru → Exit mita
	linkUser, linkPass := "", ""
	for _, u := range users {
		if u.Username != "" && u.ProxyPassword != "" {
			linkUser, linkPass = u.Username, u.ProxyPassword
			break
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
			for _, u := range users {
				if u.StickyExitID != "" && u.StickyExitID != n.ID {
					continue
				}
				cfg.Users = append(cfg.Users, model.AgentUser{
					UserID:   u.ID,
					Username: u.Username,
					Password: u.ProxyPassword,
					Enabled:  u.Status == model.StatusActive,
					MitaUser: u.Username,
				})
			}
			pmin, pmax := n.EffectivePortRange()
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mita_server",
				"config": map[string]interface{}{
					"listen_port": n.EffectiveListenPort(),
					"port_min":    pmin,
					"port_max":    pmax,
				},
			})

		case model.RoleHybrid:
			// hybrid = exit mita + public socks for entry use
			for _, u := range users {
				if u.StickyExitID != "" && u.StickyExitID != n.ID {
					continue
				}
				cfg.Users = append(cfg.Users, model.AgentUser{
					UserID:   u.ID,
					Username: u.Username,
					Password: u.ProxyPassword,
					Enabled:  u.Status == model.StatusActive,
					MitaUser: u.Username,
				})
			}
			pmin, pmax := n.EffectivePortRange()
			// mita on listen_port; socks on listen_port+1 to avoid conflict
			mitaPort := n.EffectiveListenPort()
			socksPort := mitaPort
			if pmax > pmin {
				socksPort = pmin
				mitaPort = pmin + 1
				if mitaPort > pmax {
					mitaPort = pmax
				}
			} else {
				// single port: only mita; socks shares not possible
				socksPort = 0
			}
			cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
				"type": "mita_server",
				"config": map[string]interface{}{
					"listen_port": mitaPort,
					"port_min":    mitaPort,
					"port_max":    mitaPort,
				},
			})
			if socksPort > 0 && socksPort != mitaPort {
				cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
					"type": "socks_in",
					"config": map[string]interface{}{
						"auth":        "users",
						"listen_port": socksPort,
						"port_min":    socksPort,
						"port_max":    socksPort,
					},
				})
			}

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
				// match if any hop is this entry, or first non-external is this
				involved := false
				for _, h := range hops {
					if h.NodeID == n.ID {
						involved = true
						break
					}
				}
				if !involved {
					// also if users bound to this route and entry is first agent hop missing — skip
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
					upPort = next.EffectiveListenPort()
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
						// entry may not be in hops (external only) — use first agent hop as upstream target for pure entry node unused
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
						upPort = next.EffectiveListenPort()
						break
					}
				}
			}

			emin, emax := n.EffectivePortRange()
			socksCfg := map[string]interface{}{
				"auth":        "users",
				"listen_port": n.EffectiveListenPort(),
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

			exits := map[string]model.Node{}
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
							exits[ex.ID] = *ex
						}
					}
				}
			}

			pmin, pmax := n.EffectivePortRange()
			socksPortLocal := 19080

			for _, ex := range exits {
				host := ex.PublicIP
				if host == "" {
					host = ex.Hostname
				}
				if host == "" {
					continue
				}
				mcfg := map[string]interface{}{
					"server":      host,
					"port":        ex.EffectiveListenPort(),
					"exit_id":     ex.ID,
					"socks5_port": socksPortLocal,
					"rpc_port":    8964,
				}
				if linkUser != "" {
					mcfg["link_user"] = linkUser
					mcfg["link_password"] = linkPass
				}
				cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
					"type":   "mieru_client",
					"config": mcfg,
				})
				break
			}

			socksCfg := map[string]interface{}{
				"auth":        "users",
				"listen_port": n.EffectiveListenPort(),
				"port_min":    pmin,
				"port_max":    pmax,
			}
			if len(exits) > 0 {
				socksCfg["upstream_host"] = "127.0.0.1"
				socksCfg["upstream_port"] = socksPortLocal
				socksCfg["via"] = "mieru_client"
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
