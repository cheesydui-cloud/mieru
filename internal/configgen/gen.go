package configgen

import (
	"encoding/json"
	"fmt"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

// Builder builds per-node desired configs from routes + users.
// Exit nodes get mita users (metering authority).
// Entry/relay get forward hints and allowlists.
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

	for _, n := range nodes {
		cfg := model.AgentDesiredConfig{
			NodeID:  n.ID,
			Role:    n.Role,
			Plugins: []map[string]interface{}{},
			Users:   []model.AgentUser{},
			ACL:     map[string]interface{}{},
		}

		switch n.Role {
		case model.RoleExit, model.RoleHybrid:
			for _, u := range users {
				// sticky exit filter if set
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
						"users_from":  "panel",
						"metering":    "local",
						"listen_port": n.EffectiveListenPort(),
						"port_min":    pmin,
						"port_max":    pmax,
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
			// derive simple forwards from first hop of assigned routes
			forwards := []model.ForwardRule{}
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
				if len(hops) < 2 {
					continue
				}
				// hop[0] should be entry, hop[1] next
				nextID := hops[1].NodeID
				next, err := b.Store.GetNode(nextID)
				if err != nil {
					continue
				}
				host := next.PublicIP
				if next.Hostname != "" {
					// for node-to-node, prefer IP if present
					if next.PublicIP != "" {
						host = next.PublicIP
					} else {
						host = next.Hostname
					}
				}
					lp := n.PortInRange(u.ID)
					tp := next.PortInRange(u.ID)
					forwards = append(forwards, model.ForwardRule{
						ListenPort: lp,
						TargetHost: host,
						TargetPort: tp,
						Protocol:   "tcp",
						Comment:    fmt.Sprintf("user %s -> %s", u.Username, next.Name),
					})
				}
				cfg.Forwards = forwards
				emin, emax := n.EffectivePortRange()
				cfg.Plugins = append(cfg.Plugins,
					map[string]interface{}{
						"type": "socks_in",
						"config": map[string]interface{}{
							"auth":        "users",
							"listen_port": n.EffectiveListenPort(),
							"port_min":    emin,
							"port_max":    emax,
						},
					},
					map[string]interface{}{"type": "nft_forward", "config": map[string]interface{}{"rules_from": "forwards"}},
				)
		case model.RoleRelay:
			// mieru client toward exits found in routes
			exits := map[string]model.Node{}
			for _, r := range routes {
				if !r.Enabled {
					continue
				}
				var hops []model.Hop
				_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
				for _, h := range hops {
					if h.CapabilityType == "mita_server" || h.Order == len(hops)-1 {
						if ex, err := b.Store.GetNode(h.NodeID); err == nil && (ex.Role == model.RoleExit || ex.Role == model.RoleHybrid) {
							exits[ex.ID] = *ex
						}
					}
				}
			}
				pmin, pmax := n.EffectivePortRange()
				for _, ex := range exits {
					host := ex.PublicIP
					if host == "" {
						host = ex.Hostname
					}
					cfg.Plugins = append(cfg.Plugins, map[string]interface{}{
						"type": "mieru_client",
						"config": map[string]interface{}{
							"server":      host,
							"port":        ex.EffectiveListenPort(),
							"exit_id":     ex.ID,
							"listen_port": n.EffectiveListenPort(),
							"port_min":    pmin,
							"port_max":    pmax,
						},
					})
				}
			for _, u := range users {
				cfg.Users = append(cfg.Users, model.AgentUser{
					UserID:   u.ID,
					Username: u.Username,
					Password: u.ProxyPassword,
					Enabled:  true,
					MitaUser: u.Username,
				})
			}
		}

		raw, _ := json.Marshal(cfg)
		ver, err := b.Store.BumpNodeConfigVersion(n.ID)
		if err != nil {
			return err
		}
		cfg.Version = ver
		raw, _ = json.Marshal(cfg)
		if err := b.Store.SaveDesiredConfig(n.ID, ver, string(raw)); err != nil {
			return err
		}
	}
	return nil
}
