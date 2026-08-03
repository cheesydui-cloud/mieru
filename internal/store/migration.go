package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/model"
)

const (
	MigrationFormat        = "mieru-panel-migration"
	MigrationFormatVersion = 1
)

// MigrationAdmin is admin row with password_hash exposed for migration JSON only.
type MigrationAdmin struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	CreatedAt    time.Time `json:"created_at,omitempty"`
}

// MigrationNode is a full node including agent_token (json:"-"` on model.Node).
type MigrationNode struct {
	ID            string     `json:"id"`
	Name          string     `json:"name"`
	Role          string     `json:"role"`
	Region        string     `json:"region"`
	Tags          string     `json:"tags"`
	PublicIP      string     `json:"public_ip"`
	PrivateIP     string     `json:"private_ip"`
	Hostname      string     `json:"hostname"`
	AltHostnames  string     `json:"alt_hostnames"`
	AgentToken    string     `json:"agent_token"`
	Status        string     `json:"status"`
	LastSeen      *time.Time `json:"last_seen,omitempty"`
	ConfigVersion int64      `json:"config_version"`
	MetaJSON      string     `json:"meta_json,omitempty"`
	ListenPort    int        `json:"listen_port"`
	PortMin       int        `json:"port_min"`
	PortMax       int        `json:"port_max"`
	CreatedAt     time.Time  `json:"created_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at,omitempty"`
}

// MigrationUser includes proxy_password for restore (ListUsers normally hides it).
type MigrationUser struct {
	ID                int64      `json:"id"`
	Username          string     `json:"username"`
	ProxyPassword     string     `json:"proxy_password"`
	Status            string     `json:"status"`
	ExpireAt          *time.Time `json:"expire_at,omitempty"`
	TrafficLimitBytes int64      `json:"traffic_limit_bytes"`
	TrafficUsedBytes  int64      `json:"traffic_used_bytes"`
	SpeedLimitBps     int64      `json:"speed_limit_bps"`
	MaxSessions       int        `json:"max_sessions"`
	StickyExitID      string     `json:"sticky_exit_id,omitempty"`
	SubToken          string     `json:"sub_token"`
	RouteID           *int64     `json:"route_id,omitempty"`
	EntryHost         string     `json:"entry_host,omitempty"`
	EntryPort         int        `json:"entry_port,omitempty"`
	Note              string     `json:"note"`
	DisplayMultiplier float64    `json:"display_multiplier,omitempty"`
	CreatedAt         time.Time  `json:"created_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at,omitempty"`
}

// MigrationTrafficHourly is a traffic_hourly row (hour as RFC3339 string for stable JSON).
type MigrationTrafficHourly struct {
	UserID     int64  `json:"user_id"`
	ExitNodeID string `json:"exit_node_id"`
	RouteID    *int64 `json:"route_id,omitempty"`
	Hour       string `json:"hour"`
	UpBytes    int64  `json:"up_bytes"`
	DownBytes  int64  `json:"down_bytes"`
}

// MigrationSnapshot is the full panel migration package (contains secrets).
type MigrationSnapshot struct {
	Format          string                   `json:"format"`
	FormatVersion   int                      `json:"format_version"`
	ExportedAt      string                   `json:"exported_at"`
	PanelVersion    string                   `json:"panel_version,omitempty"`
	SecretsIncluded bool                     `json:"secrets_included"`
	Settings        map[string]string        `json:"settings"`
	Admins          []MigrationAdmin         `json:"admins"`
	Nodes           []MigrationNode          `json:"nodes"`
	Routes          []model.Route            `json:"routes"`
	Users           []MigrationUser          `json:"users"`
	Announcements   []model.Announcement     `json:"announcements"`
	TrafficHourly   []MigrationTrafficHourly `json:"traffic_hourly"`
	Note            string                   `json:"note,omitempty"`
}

// ExportMigration builds a full secret-inclusive snapshot for panel move.
func (s *Store) ExportMigration(panelVersion string) (*MigrationSnapshot, error) {
	settings, err := s.listAllSettings()
	if err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	admins, err := s.listAdminsForMigration()
	if err != nil {
		return nil, fmt.Errorf("admins: %w", err)
	}
	nodes, err := s.listNodesForMigration()
	if err != nil {
		return nil, fmt.Errorf("nodes: %w", err)
	}
	routes, err := s.ListRoutes()
	if err != nil {
		return nil, fmt.Errorf("routes: %w", err)
	}
	users, err := s.listUsersForMigration()
	if err != nil {
		return nil, fmt.Errorf("users: %w", err)
	}
	anns, err := s.ListAnnouncements()
	if err != nil {
		return nil, fmt.Errorf("announcements: %w", err)
	}
	traffic, err := s.listTrafficHourlyForMigration()
	if err != nil {
		return nil, fmt.Errorf("traffic: %w", err)
	}
	return &MigrationSnapshot{
		Format:          MigrationFormat,
		FormatVersion:   MigrationFormatVersion,
		ExportedAt:      time.Now().UTC().Format(time.RFC3339),
		PanelVersion:    panelVersion,
		SecretsIncluded: true,
		Settings:        settings,
		Admins:          admins,
		Nodes:           nodes,
		Routes:          routes,
		Users:           users,
		Announcements:   anns,
		TrafficHourly:   traffic,
		Note:            "含密钥（agent_token / 用户代理密码 / 管理员哈希 / backbone / CF token）。仅用于面板搬迁，勿外传。",
	}, nil
}

// ValidateMigrationSnapshot checks package shape before import.
func ValidateMigrationSnapshot(snap *MigrationSnapshot) error {
	if snap == nil {
		return fmt.Errorf("空迁移包")
	}
	if snap.Format != MigrationFormat {
		return fmt.Errorf("不支持的格式 %q（期望 %s）", snap.Format, MigrationFormat)
	}
	if snap.FormatVersion != MigrationFormatVersion {
		return fmt.Errorf("不支持的 format_version %d（期望 %d）", snap.FormatVersion, MigrationFormatVersion)
	}
	if !snap.SecretsIncluded {
		return fmt.Errorf("迁移包未标记 secrets_included，拒绝导入（请用「完整迁移」导出）")
	}
	for i, n := range snap.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return fmt.Errorf("nodes[%d]: 缺少 id", i)
		}
		if strings.TrimSpace(n.AgentToken) == "" {
			return fmt.Errorf("nodes[%d] %s: 缺少 agent_token", i, n.ID)
		}
		if strings.TrimSpace(n.Name) == "" {
			return fmt.Errorf("nodes[%d] %s: 缺少 name", i, n.ID)
		}
		if strings.TrimSpace(n.Role) == "" {
			return fmt.Errorf("nodes[%d] %s: 缺少 role", i, n.ID)
		}
	}
	for i, u := range snap.Users {
		if u.ID <= 0 {
			return fmt.Errorf("users[%d]: 无效 id", i)
		}
		if strings.TrimSpace(u.Username) == "" {
			return fmt.Errorf("users[%d]: 缺少 username", i)
		}
		if strings.TrimSpace(u.ProxyPassword) == "" {
			return fmt.Errorf("users[%d] %s: 缺少 proxy_password", i, u.Username)
		}
		if strings.TrimSpace(u.SubToken) == "" {
			return fmt.Errorf("users[%d] %s: 缺少 sub_token", i, u.Username)
		}
	}
	for i, r := range snap.Routes {
		if r.ID <= 0 {
			return fmt.Errorf("routes[%d]: 无效 id", i)
		}
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("routes[%d]: 缺少 name", i)
		}
	}
	for i, a := range snap.Admins {
		if strings.TrimSpace(a.Username) == "" || strings.TrimSpace(a.PasswordHash) == "" {
			return fmt.Errorf("admins[%d]: 缺少 username 或 password_hash", i)
		}
	}
	if len(snap.Admins) == 0 {
		return fmt.Errorf("迁移包无管理员账号，拒绝导入")
	}
	return nil
}

// ImportMigration replaces panel data with the snapshot in one transaction.
// Does not rebuild agent desired configs — caller should RebuildAll after success.
func (s *Store) ImportMigration(snap *MigrationSnapshot) error {
	if err := ValidateMigrationSnapshot(snap); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Order: children first, then parents.
	for _, q := range []string{
		`DELETE FROM traffic_hourly`,
		`DELETE FROM users`,
		`DELETE FROM routes`,
		`DELETE FROM node_desired_config`,
		`DELETE FROM capabilities`,
		`DELETE FROM nodes`,
		`DELETE FROM announcements`,
		`DELETE FROM settings`,
		`DELETE FROM admins`,
		// keep audit_logs (history of this panel instance)
	} {
		if _, err := tx.Exec(q); err != nil {
			return fmt.Errorf("清空 %s: %w", q, err)
		}
	}

	// settings
	if snap.Settings == nil {
		snap.Settings = map[string]string{}
	}
	for k, v := range snap.Settings {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, k, v); err != nil {
			return fmt.Errorf("settings %s: %w", k, err)
		}
	}

	// admins
	for _, a := range snap.Admins {
		ts := now()
		if !a.CreatedAt.IsZero() {
			ts = a.CreatedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(
			`INSERT INTO admins(username, password_hash, created_at) VALUES(?,?,?)`,
			strings.TrimSpace(a.Username), a.PasswordHash, ts,
		); err != nil {
			return fmt.Errorf("admin %s: %w", a.Username, err)
		}
	}

	// nodes (preserve id + agent_token)
	for _, n := range snap.Nodes {
		var lastSeen any
		if n.LastSeen != nil {
			lastSeen = n.LastSeen.UTC().Format(time.RFC3339)
		}
		created := now()
		updated := created
		if !n.CreatedAt.IsZero() {
			created = n.CreatedAt.UTC().Format(time.RFC3339)
		}
		if !n.UpdatedAt.IsZero() {
			updated = n.UpdatedAt.UTC().Format(time.RFC3339)
		}
		status := n.Status
		if status == "" {
			status = model.StatusOffline
		}
		cv := n.ConfigVersion
		if cv <= 0 {
			cv = 1
		}
		if _, err := tx.Exec(
			`INSERT INTO nodes(id,name,role,region,tags,public_ip,private_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at,listen_port,port_min,port_max)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ID, n.Name, n.Role, n.Region, n.Tags, n.PublicIP, n.PrivateIP, n.Hostname, n.AltHostnames, n.AgentToken,
			status, lastSeen, cv, n.MetaJSON, created, updated, n.ListenPort, n.PortMin, n.PortMax,
		); err != nil {
			return fmt.Errorf("node %s: %w", n.ID, err)
		}
	}

	// routes (preserve id)
	for _, r := range snap.Routes {
		strategy := r.Strategy
		if strategy == "" {
			strategy = "sticky"
		}
		hops := r.HopsJSON
		if hops == "" {
			hops = "[]"
		}
		weight := r.Weight
		if weight == 0 {
			weight = 100
		}
		health := r.Health
		if health == "" {
			health = "unknown"
		}
		created := now()
		updated := created
		if !r.CreatedAt.IsZero() {
			created = r.CreatedAt.UTC().Format(time.RFC3339)
		}
		if !r.UpdatedAt.IsZero() {
			updated = r.UpdatedAt.UTC().Format(time.RFC3339)
		}
		if _, err := tx.Exec(
			`INSERT INTO routes(id,name,enabled,strategy,hops_json,weight,health,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`,
			r.ID, r.Name, boolToInt(r.Enabled), strategy, hops, weight, health, created, updated,
		); err != nil {
			return fmt.Errorf("route %d: %w", r.ID, err)
		}
	}

	// users (preserve id, proxy_password, sub_token)
	for _, u := range snap.Users {
		status := u.Status
		if status == "" {
			status = model.StatusActive
		}
		var exp any
		if u.ExpireAt != nil {
			exp = u.ExpireAt.UTC().Format(time.RFC3339)
		}
		mult := u.DisplayMultiplier
		if mult <= 0 {
			mult = 1
		}
		created := now()
		updated := created
		if !u.CreatedAt.IsZero() {
			created = u.CreatedAt.UTC().Format(time.RFC3339)
		}
		if !u.UpdatedAt.IsZero() {
			updated = u.UpdatedAt.UTC().Format(time.RFC3339)
		}
		hash := mustHash(u.ProxyPassword)
		if _, err := tx.Exec(
			`INSERT INTO users(id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,entry_host,entry_port,note,display_multiplier,created_at,updated_at)
			 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			u.ID, u.Username, hash, u.ProxyPassword, status, exp,
			u.TrafficLimitBytes, u.TrafficUsedBytes, u.SpeedLimitBps, u.MaxSessions, u.StickyExitID, u.SubToken,
			u.RouteID, u.EntryHost, u.EntryPort, u.Note, mult, created, updated,
		); err != nil {
			return fmt.Errorf("user %s: %w", u.Username, err)
		}
	}

	// announcements
	for _, a := range snap.Announcements {
		created := now()
		updated := created
		if !a.CreatedAt.IsZero() {
			created = a.CreatedAt.UTC().Format(time.RFC3339)
		}
		if !a.UpdatedAt.IsZero() {
			updated = a.UpdatedAt.UTC().Format(time.RFC3339)
		}
		if a.ID > 0 {
			if _, err := tx.Exec(
				`INSERT INTO announcements(id,title,body,enabled,popup,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
				a.ID, a.Title, a.Body, boolToInt(a.Enabled), boolToInt(a.Popup), created, updated,
			); err != nil {
				return fmt.Errorf("announcement %d: %w", a.ID, err)
			}
		} else {
			if _, err := tx.Exec(
				`INSERT INTO announcements(title,body,enabled,popup,created_at,updated_at) VALUES(?,?,?,?,?,?)`,
				a.Title, a.Body, boolToInt(a.Enabled), boolToInt(a.Popup), created, updated,
			); err != nil {
				return fmt.Errorf("announcement: %w", err)
			}
		}
	}

	// traffic_hourly
	for _, t := range snap.TrafficHourly {
		hour := strings.TrimSpace(t.Hour)
		if hour == "" || t.UserID <= 0 || t.ExitNodeID == "" {
			continue
		}
		if t.RouteID != nil {
			if _, err := tx.Exec(
				`INSERT INTO traffic_hourly(user_id,exit_node_id,route_id,hour,up_bytes,down_bytes) VALUES(?,?,?,?,?,?)
				 ON CONFLICT(user_id, exit_node_id, hour) DO UPDATE SET
				   up_bytes=excluded.up_bytes, down_bytes=excluded.down_bytes, route_id=excluded.route_id`,
				t.UserID, t.ExitNodeID, *t.RouteID, hour, t.UpBytes, t.DownBytes,
			); err != nil {
				return fmt.Errorf("traffic user=%d: %w", t.UserID, err)
			}
		} else {
			if _, err := tx.Exec(
				`INSERT INTO traffic_hourly(user_id,exit_node_id,hour,up_bytes,down_bytes) VALUES(?,?,?,?,?)
				 ON CONFLICT(user_id, exit_node_id, hour) DO UPDATE SET
				   up_bytes=excluded.up_bytes, down_bytes=excluded.down_bytes`,
				t.UserID, t.ExitNodeID, hour, t.UpBytes, t.DownBytes,
			); err != nil {
				return fmt.Errorf("traffic user=%d: %w", t.UserID, err)
			}
		}
	}

	// SQLite autoincrement bookkeeping after explicit IDs
	if _, err := tx.Exec(`DELETE FROM sqlite_sequence WHERE name IN ('users','routes','announcements','traffic_hourly','admins')`); err != nil {
		// sqlite_sequence may not exist if never used autoincrement — ignore
		_ = err
	}
	// reseed sequences to max(id)
	for _, table := range []string{"users", "routes", "announcements", "traffic_hourly", "admins"} {
		var maxID sql.NullInt64
		_ = tx.QueryRow(`SELECT MAX(id) FROM ` + table).Scan(&maxID)
		if maxID.Valid && maxID.Int64 > 0 {
			_, _ = tx.Exec(`INSERT INTO sqlite_sequence(name,seq) VALUES(?,?)`, table, maxID.Int64)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	// clear in-memory rates (stale after replace)
	s.mu.Lock()
	s.rates = map[int64]model.TrafficSample{}
	s.mu.Unlock()
	return nil
}

func (s *Store) listAllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) listAdminsForMigration() ([]MigrationAdmin, error) {
	rows, err := s.db.Query(`SELECT username, password_hash, created_at FROM admins ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MigrationAdmin, 0)
	for rows.Next() {
		var a MigrationAdmin
		var c string
		if err := rows.Scan(&a.Username, &a.PasswordHash, &c); err != nil {
			return nil, err
		}
		if t := parseTime(c); t != nil {
			a.CreatedAt = *t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) listNodesForMigration() ([]MigrationNode, error) {
	nodes, err := s.ListNodes()
	if err != nil {
		return nil, err
	}
	out := make([]MigrationNode, 0, len(nodes))
	for _, n := range nodes {
		// ListNodes includes AgentToken in struct (json omit only)
		out = append(out, MigrationNode{
			ID:            n.ID,
			Name:          n.Name,
			Role:          n.Role,
			Region:        n.Region,
			Tags:          n.Tags,
			PublicIP:      n.PublicIP,
			PrivateIP:     n.PrivateIP,
			Hostname:      n.Hostname,
			AltHostnames:  n.AltHostnames,
			AgentToken:    n.AgentToken,
			Status:        n.Status,
			LastSeen:      n.LastSeen,
			ConfigVersion: n.ConfigVersion,
			MetaJSON:      n.MetaJSON,
			ListenPort:    n.ListenPort,
			PortMin:       n.PortMin,
			PortMax:       n.PortMax,
			CreatedAt:     n.CreatedAt,
			UpdatedAt:     n.UpdatedAt,
		})
	}
	return out, nil
}

func (s *Store) listUsersForMigration() ([]MigrationUser, error) {
	rows, err := s.db.Query(`SELECT ` + userSelectCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MigrationUser, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, MigrationUser{
			ID:                u.ID,
			Username:          u.Username,
			ProxyPassword:     u.ProxyPassword,
			Status:            u.Status,
			ExpireAt:          u.ExpireAt,
			TrafficLimitBytes: u.TrafficLimitBytes,
			TrafficUsedBytes:  u.TrafficUsedBytes,
			SpeedLimitBps:     u.SpeedLimitBps,
			MaxSessions:       u.MaxSessions,
			StickyExitID:      u.StickyExitID,
			SubToken:          u.SubToken,
			RouteID:           u.RouteID,
			EntryHost:         u.EntryHost,
			EntryPort:         u.EntryPort,
			Note:              u.Note,
			DisplayMultiplier: u.DisplayMultiplier,
			CreatedAt:         u.CreatedAt,
			UpdatedAt:         u.UpdatedAt,
		})
	}
	return out, rows.Err()
}

func (s *Store) listTrafficHourlyForMigration() ([]MigrationTrafficHourly, error) {
	rows, err := s.db.Query(`SELECT user_id, exit_node_id, route_id, hour, up_bytes, down_bytes FROM traffic_hourly ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]MigrationTrafficHourly, 0)
	for rows.Next() {
		var t MigrationTrafficHourly
		var routeID sql.NullInt64
		var hour string
		if err := rows.Scan(&t.UserID, &t.ExitNodeID, &routeID, &hour, &t.UpBytes, &t.DownBytes); err != nil {
			return nil, err
		}
		t.Hour = hour
		if routeID.Valid {
			v := routeID.Int64
			t.RouteID = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
