package model

import (
	"strings"
	"time"
)

// Node roles are generic — not bound to any cloud vendor.
const (
	RoleEntry  = "entry"
	RoleRelay  = "relay"
	RoleExit   = "exit"
	RoleHybrid = "hybrid"
)

const (
	StatusActive    = "active"
	StatusDisabled  = "disabled"
	StatusExpired   = "expired"
	StatusOverQuota = "over_quota"
	StatusOffline   = "offline"
	StatusOnline    = "online"
	StatusDegraded  = "degraded"
)

type Admin struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

type Node struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Role     string `json:"role"` // entry|relay|exit|hybrid
	Region   string `json:"region"`
	Tags     string `json:"tags"` // comma-separated
	PublicIP string `json:"public_ip"`
	// PrivateIP is the LAN / IX internal address used by previous hops
	// (e.g. entry→relay, relay→exit on the same fabric). Empty = use PublicIP/Hostname.
	PrivateIP    string `json:"private_ip"`
	Hostname     string `json:"hostname"` // domain preferred for clients
	AltHostnames string `json:"alt_hostnames"`
	// PortMin/PortMax: node port range (start-end). Client/subscription uses PortMin as primary.
	// 0/0 = role default range. ListenPort is kept for backward compat (= PortMin when set).
	ListenPort    int        `json:"listen_port"`
	PortMin       int        `json:"port_min"`
	PortMax       int        `json:"port_max"`
	AgentToken    string     `json:"-"`
	Status        string     `json:"status"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	ConfigVersion int64      `json:"config_version"`
	MetaJSON      string     `json:"meta_json"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// NormalizePorts: UI only uses start/end; primary listen = start port.
func (n *Node) NormalizePorts() {
	if n.PortMin > 0 && n.ListenPort == 0 {
		n.ListenPort = n.PortMin
	}
	if n.ListenPort > 0 && n.PortMin == 0 {
		n.PortMin = n.ListenPort
	}
	if n.PortMin > 0 && n.PortMax == 0 {
		n.PortMax = n.PortMin
	}
	if n.PortMax > 0 && n.PortMin == 0 {
		n.PortMin = n.PortMax
		n.ListenPort = n.PortMax
	}
}

// DefaultListenPort is the public client-facing port when none is configured.
func DefaultListenPort(role string) int {
	switch role {
	case RoleEntry:
		return 1080
	case RoleHybrid:
		// Public SOCKS for clients; mita uses HybridMitaPort.
		return 1080
	case RoleRelay:
		return 8964
	case RoleExit:
		return 8964
	default:
		return 1080
	}
}

// HybridMitaPort is the private/public mita port when hybrid also exposes SOCKS.
func HybridMitaPort(socksPort int) int {
	if socksPort <= 0 {
		return 8964
	}
	// Prefer socks+1; wrap away from 65535 collision.
	p := socksPort + 1
	if p > 65535 {
		p = socksPort - 1
	}
	if p < 1 {
		p = 8964
	}
	return p
}

// EffectiveListenPort: primary public port clients / probes / upstreams use.
// Always consistent with what configgen binds for the role's public service:
//
//	entry/relay/hybrid → socks_in; exit → mita.
func (n *Node) EffectiveListenPort() int {
	if n.PortMin > 0 {
		return n.PortMin
	}
	if n.ListenPort > 0 {
		return n.ListenPort
	}
	return DefaultListenPort(n.Role)
}

// DefaultFrontPortPoolSpan is how many ports a front (relay/entry) may use for
// multi-tunnel allocation when the node only has a single listen port saved
// (legacy: port_min=port_max=10401). Matches UI default 10401–10499 (99 ports).
const DefaultFrontPortPoolSpan = 98

// EffectivePortRange returns [min,max]. When unset, defaults to a SINGLE port
// equal to EffectiveListenPort so probe/subscription/mita/socks never disagree.
//
// Front (relay/entry) is multi-tunnel by design (one listen port per tunnel).
// Historical nodes often only stored a single port (min==max). Those expand to
// min..(min+DefaultFrontPortPoolSpan) so custom hop.Port and sequential alloc
// match the panel UI — not a hardcoded 10403, just the node's pool.
func (n *Node) EffectivePortRange() (int, int) {
	min, max := n.PortMin, n.PortMax
	if min <= 0 && max <= 0 {
		p := n.EffectiveListenPort()
		min, max = p, p
	} else {
		if min <= 0 {
			min = n.EffectiveListenPort()
		}
		if max <= 0 {
			max = min
		}
		if min > max {
			min, max = max, min
		}
	}
	if min < 1 {
		min = 1
	}
	if max > 65535 {
		max = 65535
	}
	// Expand legacy single-port front into a multi-tunnel pool.
	if (n.Role == RoleRelay || n.Role == RoleEntry) && min > 0 && max <= min {
		max = min + DefaultFrontPortPoolSpan
		if max > 65535 {
			max = 65535
		}
	}
	return min, max
}

// PublicServicePort is the port the next hop / probe should connect to.
// For hybrid this is always the public SOCKS port (not mita).
func (n *Node) PublicServicePort() int {
	return n.EffectiveListenPort()
}

// MitaPrimaryPort is the port mita listens on for exit/hybrid.
func (n *Node) MitaPrimaryPort() int {
	switch n.Role {
	case RoleHybrid:
		return HybridMitaPort(n.EffectiveListenPort())
	default:
		return n.EffectiveListenPort()
	}
}

// DialHost returns the address previous hops should use to reach this node.
// Prefer private/LAN IP (IX fabric) when set; else public IP; else hostname.
func (n *Node) DialHost() string {
	if n == nil {
		return ""
	}
	if ip := strings.TrimSpace(n.PrivateIP); ip != "" {
		return ip
	}
	if ip := strings.TrimSpace(n.PublicIP); ip != "" {
		return ip
	}
	return strings.TrimSpace(n.Hostname)
}

// PublicHost is client-facing address (subscription / external clients).
// Prefer hostname, then public IP (never private_ip — clients are off-net).
func (n *Node) PublicHost() string {
	if n == nil {
		return ""
	}
	if h := strings.TrimSpace(n.Hostname); h != "" && !isPlaceholderHost(h) {
		return h
	}
	return strings.TrimSpace(n.PublicIP)
}

// ClientHost is what end-user mierus:// links / query-page entry should dial.
// Same priority as PublicHost: filled non-placeholder hostname (domain) first,
// else public IP. Never returns private_ip.
// (Operators who leave hostname empty keep IP-only behaviour.)
func (n *Node) ClientHost() string {
	return n.PublicHost()
}

// isPlaceholderHost rejects common template / example hostnames so share
// links fall back to PublicIP instead of a non-resolving name.
func isPlaceholderHost(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	if h == "" {
		return true
	}
	if strings.HasSuffix(h, ".example.com") || strings.HasSuffix(h, ".example.org") || strings.HasSuffix(h, ".example.net") {
		return true
	}
	switch h {
	case "example.com", "localhost", "127.0.0.1", "0.0.0.0", "::1":
		return true
	}
	return false
}

// PortInRange maps userID into the node's allowed port pool (stable).
func (n *Node) PortInRange(userID int64) int {
	min, max := n.EffectivePortRange()
	span := max - min + 1
	if span <= 0 {
		return min
	}
	if userID <= 0 {
		return min
	}
	return min + int((userID-1)%int64(span))
}

type Capability struct {
	ID         int64  `json:"id"`
	NodeID     string `json:"node_id"`
	Type       string `json:"type"` // nft_forward|mieru_client|mita_server|socks_in
	Enabled    bool   `json:"enabled"`
	Listen     string `json:"listen"`
	ConfigJSON string `json:"config_json"`
}

// Route is an ordered hop chain — generic multi-hop, not vendor-specific.
type Route struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Enabled   bool      `json:"enabled"`
	Strategy  string    `json:"strategy"`  // sticky|wrr|failover
	HopsJSON  string    `json:"hops_json"` // [{node_id, order, capability_type}]
	Weight    int       `json:"weight"`
	Health    string    `json:"health"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Hop is one step in a route. NodeID is empty when External is true
// (merchant-provided entry IP/domain with no Agent).
type Hop struct {
	NodeID         string `json:"node_id,omitempty"`
	Order          int    `json:"order"`
	CapabilityType string `json:"capability_type,omitempty"`
	// External entry: no panel node / no Agent; only client-facing endpoint.
	External bool   `json:"external,omitempty"`
	Host     string `json:"host,omitempty"` // IP or domain for clients
	Port     int    `json:"port,omitempty"` // client port (0 = default)
	Name     string `json:"name,omitempty"` // display name in subscription
}

type User struct {
	ID                int64      `json:"id"`
	Username          string     `json:"username"`
	PasswordHash      string     `json:"-"`
	ProxyPassword     string     `json:"proxy_password,omitempty"` // plain returned only on create/reset
	Status            string     `json:"status"`
	ExpireAt          *time.Time `json:"expire_at,omitempty"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	TrafficUsedBytes  int64      `json:"traffic_used_bytes"`
	SpeedLimitBps     int64      `json:"speed_limit_bps"`
	MaxSessions       int        `json:"max_sessions"`
	StickyExitID      string     `json:"sticky_exit_id,omitempty"`
	SubToken          string     `json:"sub_token,omitempty"`
	RouteID           *int64     `json:"route_id,omitempty"`
	// EntryHost/EntryPort: optional client-facing public entry override (IP or domain).
	// Empty host = resolve from first hop of bound route (or first entry/relay node).
	EntryHost string `json:"entry_host,omitempty"`
	EntryPort int    `json:"entry_port,omitempty"`
	Note      string `json:"note"`
	// DisplayMultiplier only affects public user-info page numbers (used/today/rate).
	// Real metering & admin list stay unscaled. Default 1.
	DisplayMultiplier float64   `json:"display_multiplier,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

type TrafficHourly struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	ExitNodeID string    `json:"exit_node_id"`
	RouteID    *int64    `json:"route_id,omitempty"`
	Hour       time.Time `json:"hour"`
	UpBytes    int64     `json:"up_bytes"`
	DownBytes  int64     `json:"down_bytes"`
}

type TrafficSample struct {
	UserID    int64 `json:"user_id"`
	UpBps     int64 `json:"up_bps"`
	DownBps   int64 `json:"down_bps"`
	UpBytes   int64 `json:"up_bytes"`
	DownBytes int64 `json:"down_bytes"`
	TS        int64 `json:"ts"`
}

type AuditLog struct {
	ID        int64     `json:"id"`
	Actor     string    `json:"actor"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	CreatedAt time.Time `json:"created_at"`
}

// AgentDesiredConfig is what the panel pushes/pulls to agents.
type AgentDesiredConfig struct {
	Version  int64                    `json:"version"`
	NodeID   string                   `json:"node_id"`
	Role     string                   `json:"role"`
	Plugins  []map[string]interface{} `json:"plugins"`
	Users    []AgentUser              `json:"users,omitempty"` // exit: mita users; entry: allowlist
	ACL      map[string]interface{}   `json:"acl,omitempty"`
	Forwards []ForwardRule            `json:"forwards,omitempty"`
}

type AgentUser struct {
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  bool   `json:"enabled"`
	// Exit metering identity
	MitaUser string `json:"mita_user,omitempty"`
	// SpeedLimitBps is max total throughput (up+down) in bytes/sec; 0 = unlimited.
	// Stored for agent / future shaping; mita has no native bandwidth field.
	SpeedLimitBps int64 `json:"speed_limit_bps,omitempty"`
}

type ForwardRule struct {
	ListenPort int    `json:"listen_port"`
	TargetHost string `json:"target_host"`
	TargetPort int    `json:"target_port"`
	Protocol   string `json:"protocol"` // tcp
	Comment    string `json:"comment"`
}

type HeartbeatRequest struct {
	NodeID        string  `json:"node_id"`
	Token         string  `json:"token"`
	Role          string  `json:"role"`
	ConfigVersion int64   `json:"config_version"`
	CPU           float64 `json:"cpu"`
	MemPct        float64 `json:"mem_pct"`
	AgentVersion  string  `json:"agent_version"`
	PublicIP      string  `json:"public_ip"`
	Hostname      string  `json:"hostname"`
	Message       string  `json:"message"`
	// ApplyError is the last plugin apply failure (empty = healthy).
	// Shown on panel so operators don't need SSH to journalctl.
	ApplyError string `json:"apply_error,omitempty"`
}

type TrafficReport struct {
	NodeID  string                `json:"node_id"`
	Token   string                `json:"token"`
	Samples []TrafficReportSample `json:"samples"`
}

type TrafficReportSample struct {
	UserID    int64 `json:"user_id"`
	UpDelta   int64 `json:"up_delta"`
	DownDelta int64 `json:"down_delta"`
	UpBps     int64 `json:"up_bps"`
	DownBps   int64 `json:"down_bps"`
	TS        int64 `json:"ts"`
}

type DashboardStats struct {
	OnlineNodes    int   `json:"online_nodes"`
	TotalNodes     int   `json:"total_nodes"`
	ActiveUsers    int   `json:"active_users"`
	TotalUsers     int   `json:"total_users"`
	TodayUp        int64 `json:"today_up"`
	TodayDown      int64 `json:"today_down"`
	UnhealthyNodes int   `json:"unhealthy_nodes"`
}

// Announcement is a panel notice shown on the public user query page.
type Announcement struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Enabled bool   `json:"enabled"`
	// Popup: at most one enabled announcement should be popup; admin chooses which.
	Popup     bool      `json:"popup"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
