package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB

	// in-memory realtime rates from exit agents
	mu    sync.RWMutex
	rates map[int64]model.TrafficSample // user_id -> latest
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, rates: map[int64]model.TrafficSample{}}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	schema := `
CREATE TABLE IF NOT EXISTS admins (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS nodes (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  role TEXT NOT NULL,
  region TEXT DEFAULT '',
  tags TEXT DEFAULT '',
  public_ip TEXT DEFAULT '',
  private_ip TEXT DEFAULT '',
  hostname TEXT DEFAULT '',
  alt_hostnames TEXT DEFAULT '',
  agent_token TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen TEXT,
  config_version INTEGER NOT NULL DEFAULT 1,
  meta_json TEXT DEFAULT '{}',
  listen_port INTEGER NOT NULL DEFAULT 0,
  port_min INTEGER NOT NULL DEFAULT 0,
  port_max INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS capabilities (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  node_id TEXT NOT NULL,
  type TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  listen TEXT DEFAULT '',
  config_json TEXT DEFAULT '{}',
  FOREIGN KEY(node_id) REFERENCES nodes(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS routes (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  strategy TEXT NOT NULL DEFAULT 'sticky',
  hops_json TEXT NOT NULL DEFAULT '[]',
  weight INTEGER NOT NULL DEFAULT 100,
  health TEXT NOT NULL DEFAULT 'unknown',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  proxy_password TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  expire_at TEXT,
  traffic_limit_bytes INTEGER NOT NULL DEFAULT 0,
  traffic_used_bytes INTEGER NOT NULL DEFAULT 0,
  speed_limit_bps INTEGER NOT NULL DEFAULT 0,
  max_sessions INTEGER NOT NULL DEFAULT 0,
  sticky_exit_id TEXT DEFAULT '',
  sub_token TEXT NOT NULL UNIQUE,
  route_id INTEGER,
entry_host TEXT DEFAULT '',
	  entry_port INTEGER NOT NULL DEFAULT 0,
	  note TEXT DEFAULT '',
	  display_multiplier REAL NOT NULL DEFAULT 1,
	  created_at TEXT NOT NULL,
	  updated_at TEXT NOT NULL
	);

CREATE TABLE IF NOT EXISTS traffic_hourly (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL,
  exit_node_id TEXT NOT NULL,
  route_id INTEGER,
  hour TEXT NOT NULL,
  up_bytes INTEGER NOT NULL DEFAULT 0,
  down_bytes INTEGER NOT NULL DEFAULT 0,
  UNIQUE(user_id, exit_node_id, hour)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  target TEXT NOT NULL,
  detail TEXT DEFAULT '',
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS node_desired_config (
  node_id TEXT PRIMARY KEY,
  version INTEGER NOT NULL,
  config_json TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS announcements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  popup INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// lightweight migrations for existing DBs
	for _, q := range []string{
		`ALTER TABLE nodes ADD COLUMN listen_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN port_min INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN port_max INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE nodes ADD COLUMN private_ip TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN entry_host TEXT DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN entry_port INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN display_multiplier REAL NOT NULL DEFAULT 1`,
		`CREATE TABLE IF NOT EXISTS announcements (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  title TEXT NOT NULL DEFAULT '',
  body TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  popup INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
)`,
		} {
			_, _ = s.db.Exec(q) // ignore "duplicate column" on fresh DBs
		}
	return nil
}

// ---------- Settings ----------

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetSettings(keys ...string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, err := s.GetSetting(k)
		if err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, nil
}

func (s *Store) PanelBaseURL() string {
	v, _ := s.GetSetting("panel_url")
	return strings.TrimRight(strings.TrimSpace(v), "/")
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil
	}
	return &t
}

func mustHash(pw string) string {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func (s *Store) EnsureAdmin(username, password string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM admins`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO admins(username, password_hash, created_at) VALUES(?,?,?)`,
		username, mustHash(password), now())
	return err
}

// SetAdminPassword creates or overwrites the admin password (used by --reset-admin).
func (s *Store) SetAdminPassword(username, password string) error {
	if username == "" || password == "" {
		return fmt.Errorf("username and password required")
	}
	hash := mustHash(password)
	var id int64
	err := s.db.QueryRow(`SELECT id FROM admins WHERE username=?`, username).Scan(&id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE admins SET password_hash=? WHERE id=?`, hash, id)
		return err
	}
	// no row with that username: if table empty insert; else update first admin + rename
	var n int
	if err2 := s.db.QueryRow(`SELECT COUNT(1) FROM admins`).Scan(&n); err2 != nil {
		return err2
	}
	if n == 0 {
		_, err = s.db.Exec(`INSERT INTO admins(username, password_hash, created_at) VALUES(?,?,?)`,
			username, hash, now())
		return err
	}
	_, err = s.db.Exec(`UPDATE admins SET username=?, password_hash=? WHERE id=(SELECT id FROM admins ORDER BY id LIMIT 1)`,
		username, hash)
	return err
}

func (s *Store) GetAdminByUsername(username string) (*model.Admin, error) {
	row := s.db.QueryRow(`SELECT id, username, password_hash, created_at FROM admins WHERE username=?`, username)
	var a model.Admin
	var created string
	if err := row.Scan(&a.ID, &a.Username, &a.PasswordHash, &created); err != nil {
		return nil, err
	}
	if t := parseTime(created); t != nil {
		a.CreatedAt = *t
	}
	return &a, nil
}

// ---------- Nodes ----------

func (s *Store) CreateNode(n *model.Node) error {
	if n.ID == "" {
		n.ID = "n_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	}
	if n.AgentToken == "" {
		n.AgentToken = "tok_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if n.Status == "" {
		n.Status = model.StatusOffline
	}
	if n.ConfigVersion == 0 {
		n.ConfigVersion = 1
	}
	ts := now()
	_, err := s.db.Exec(`INSERT INTO nodes(id,name,role,region,tags,public_ip,private_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at,listen_port,port_min,port_max)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.Role, n.Region, n.Tags, n.PublicIP, n.PrivateIP, n.Hostname, n.AltHostnames, n.AgentToken,
		n.Status, nil, n.ConfigVersion, n.MetaJSON, ts, ts, n.ListenPort, n.PortMin, n.PortMax)
	if err != nil {
		return err
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339, ts)
	n.UpdatedAt = n.CreatedAt
	return nil
}

func (s *Store) UpdateNode(n *model.Node) error {
	ts := now()
	_, err := s.db.Exec(`UPDATE nodes SET name=?, role=?, region=?, tags=?, public_ip=?, private_ip=?, hostname=?, alt_hostnames=?, status=?, meta_json=?, listen_port=?, port_min=?, port_max=?, updated_at=? WHERE id=?`,
		n.Name, n.Role, n.Region, n.Tags, n.PublicIP, n.PrivateIP, n.Hostname, n.AltHostnames, n.Status, n.MetaJSON, n.ListenPort, n.PortMin, n.PortMax, ts, n.ID)
	return err
}

func (s *Store) BumpNodeConfigVersion(nodeID string) (int64, error) {
	ts := now()
	_, err := s.db.Exec(`UPDATE nodes SET config_version = config_version + 1, updated_at=? WHERE id=?`, ts, nodeID)
	if err != nil {
		return 0, err
	}
	var v int64
	err = s.db.QueryRow(`SELECT config_version FROM nodes WHERE id=?`, nodeID).Scan(&v)
	return v, err
}

func (s *Store) GetNode(id string) (*model.Node, error) {
	row := s.db.QueryRow(`SELECT id,name,role,region,tags,public_ip,private_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at,listen_port,port_min,port_max FROM nodes WHERE id=?`, id)
	return scanNode(row)
}

func (s *Store) GetNodeByToken(id, token string) (*model.Node, error) {
	n, err := s.GetNode(id)
	if err != nil {
		return nil, err
	}
	if n.AgentToken != token {
		return nil, fmt.Errorf("invalid token")
	}
	return n, nil
}

func (s *Store) ListNodes() ([]model.Node, error) {
	rows, err := s.db.Query(`SELECT id,name,role,region,tags,public_ip,private_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at,listen_port,port_min,port_max FROM nodes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Node, 0)
	for rows.Next() {
		n, err := scanNodeRows(rows)
		if err != nil {
			return nil, err
		}
		// hide token in list by default — API can re-attach if needed
		out = append(out, *n)
	}
	return out, nil
}

// DeleteNodeResult summarizes side effects of removing a node.
type DeleteNodeResult struct {
	RoutesDeleted int `json:"routes_deleted"`
	RoutesUpdated int `json:"routes_updated"`
	UsersUnbound  int `json:"users_unbound"`
}

// DeleteNode removes the node and deactivates related data plane:
//   - deletes tunnels that hop through this node
//   - strips this node from remaining tunnels' hops
//   - clears users bound to deleted tunnels / sticky to this exit
//   - drops desired config so a re-created id starts clean
//
// Live agents lose auth on next heartbeat (401) and stop plugins.
func (s *Store) DeleteNode(id string) (*DeleteNodeResult, error) {
	res := &DeleteNodeResult{}
	if strings.TrimSpace(id) == "" {
		return res, fmt.Errorf("empty node id")
	}
	n, err := s.GetNode(id)
	if err != nil {
		return res, err
	}
	_ = n

	routes, err := s.ListRoutes()
	if err != nil {
		return res, err
	}
	deletedRouteIDs := map[int64]bool{}
	for _, r := range routes {
		var hops []model.Hop
		if err := json.Unmarshal([]byte(r.HopsJSON), &hops); err != nil {
			continue
		}
		touches := false
		kept := make([]model.Hop, 0, len(hops))
		for _, h := range hops {
			if h.NodeID == id {
				touches = true
				continue
			}
			kept = append(kept, h)
		}
		if !touches {
			continue
		}
		// Any hop removed: drop the whole tunnel (ports free, users unbind).
		// Multi-hop without front or exit is not a usable path.
		if err := s.DeleteRoute(r.ID); err != nil {
			return res, err
		}
		deletedRouteIDs[r.ID] = true
		res.RoutesDeleted++
	}

	// Unbind users on deleted tunnels; clear sticky exit pointing at this node.
	ts := now()
	if len(deletedRouteIDs) > 0 {
		for rid := range deletedRouteIDs {
			r, err := s.db.Exec(`UPDATE users SET route_id=NULL, updated_at=? WHERE route_id=?`, ts, rid)
			if err != nil {
				return res, err
			}
			if n, _ := r.RowsAffected(); n > 0 {
				res.UsersUnbound += int(n)
			}
		}
	}
	r2, err := s.db.Exec(`UPDATE users SET sticky_exit_id='', updated_at=? WHERE sticky_exit_id=?`, ts, id)
	if err != nil {
		return res, err
	}
	if n, _ := r2.RowsAffected(); n > 0 {
		// may double-count with route unbind on same row — OK for summary
		res.UsersUnbound += int(n)
	}

	_, _ = s.db.Exec(`DELETE FROM node_desired_config WHERE node_id=?`, id)
	_, _ = s.db.Exec(`DELETE FROM capabilities WHERE node_id=?`, id)
	if _, err := s.db.Exec(`DELETE FROM nodes WHERE id=?`, id); err != nil {
		return res, err
	}
	return res, nil
}

func (s *Store) Heartbeat(nodeID string, publicIP, hostname string, status string) error {
	ts := now()
	// Empty status = preserve existing (meta-only patches from panel jobs).
	if strings.TrimSpace(status) == "" {
		_, err := s.db.Exec(`UPDATE nodes SET last_seen=?, public_ip=CASE WHEN ?!='' THEN ? ELSE public_ip END, hostname=CASE WHEN ?!='' THEN ? ELSE hostname END, updated_at=? WHERE id=?`,
			ts, publicIP, publicIP, hostname, hostname, ts, nodeID)
		return err
	}
	_, err := s.db.Exec(`UPDATE nodes SET last_seen=?, status=?, public_ip=CASE WHEN ?!='' THEN ? ELSE public_ip END, hostname=CASE WHEN ?!='' THEN ? ELSE hostname END, updated_at=? WHERE id=?`,
		ts, status, publicIP, publicIP, hostname, hostname, ts, nodeID)
	return err
}

// HeartbeatEx updates status + optional meta patch (agent_version, apply_error).
func (s *Store) HeartbeatEx(nodeID string, publicIP, hostname, status string, metaPatch map[string]string) error {
	if err := s.Heartbeat(nodeID, publicIP, hostname, status); err != nil {
		return err
	}
	if len(metaPatch) == 0 {
		return nil
	}
	n, err := s.GetNode(nodeID)
	if err != nil {
		return err
	}
	meta := map[string]interface{}{}
	if n.MetaJSON != "" {
		_ = json.Unmarshal([]byte(n.MetaJSON), &meta)
	}
	if meta == nil {
		meta = map[string]interface{}{}
	}
	for k, v := range metaPatch {
		if v == "" {
			delete(meta, k)
		} else {
			meta[k] = v
		}
	}
	raw, _ := json.Marshal(meta)
	_, err = s.db.Exec(`UPDATE nodes SET meta_json=?, updated_at=? WHERE id=?`, string(raw), now(), nodeID)
	return err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanNode(row scannable) (*model.Node, error) {
	var n model.Node
	var lastSeen, created, updated sql.NullString
	var meta sql.NullString
	if err := row.Scan(&n.ID, &n.Name, &n.Role, &n.Region, &n.Tags, &n.PublicIP, &n.PrivateIP, &n.Hostname, &n.AltHostnames, &n.AgentToken, &n.Status, &lastSeen, &n.ConfigVersion, &meta, &created, &updated, &n.ListenPort, &n.PortMin, &n.PortMax); err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		n.LastSeen = parseTime(lastSeen.String)
	}
	if meta.Valid {
		n.MetaJSON = meta.String
	}
	if created.Valid {
		if t := parseTime(created.String); t != nil {
			n.CreatedAt = *t
		}
	}
	if updated.Valid {
		if t := parseTime(updated.String); t != nil {
			n.UpdatedAt = *t
		}
	}
	return &n, nil
}

func scanNodeRows(rows *sql.Rows) (*model.Node, error) {
	return scanNode(rows)
}

// ---------- Routes ----------

func (s *Store) CreateRoute(r *model.Route) error {
	ts := now()
	if r.Strategy == "" {
		r.Strategy = "sticky"
	}
	if r.HopsJSON == "" {
		r.HopsJSON = "[]"
	}
	if r.Weight == 0 {
		r.Weight = 100
	}
	res, err := s.db.Exec(`INSERT INTO routes(name,enabled,strategy,hops_json,weight,health,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		r.Name, boolToInt(r.Enabled), r.Strategy, r.HopsJSON, r.Weight, "unknown", ts, ts)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	r.ID = id
	return nil
}

func (s *Store) UpdateRoute(r *model.Route) error {
	ts := now()
	_, err := s.db.Exec(`UPDATE routes SET name=?, enabled=?, strategy=?, hops_json=?, weight=?, health=?, updated_at=? WHERE id=?`,
		r.Name, boolToInt(r.Enabled), r.Strategy, r.HopsJSON, r.Weight, r.Health, ts, r.ID)
	return err
}

func (s *Store) ListRoutes() ([]model.Route, error) {
	rows, err := s.db.Query(`SELECT id,name,enabled,strategy,hops_json,weight,health,created_at,updated_at FROM routes ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Route, 0)
	for rows.Next() {
		var r model.Route
		var en int
		var c, u string
		if err := rows.Scan(&r.ID, &r.Name, &en, &r.Strategy, &r.HopsJSON, &r.Weight, &r.Health, &c, &u); err != nil {
			return nil, err
		}
		r.Enabled = en == 1
		if t := parseTime(c); t != nil {
			r.CreatedAt = *t
		}
		if t := parseTime(u); t != nil {
			r.UpdatedAt = *t
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Store) GetRoute(id int64) (*model.Route, error) {
	row := s.db.QueryRow(`SELECT id,name,enabled,strategy,hops_json,weight,health,created_at,updated_at FROM routes WHERE id=?`, id)
	var r model.Route
	var en int
	var c, u string
	if err := row.Scan(&r.ID, &r.Name, &en, &r.Strategy, &r.HopsJSON, &r.Weight, &r.Health, &c, &u); err != nil {
		return nil, err
	}
	r.Enabled = en == 1
	return &r, nil
}

func (s *Store) DeleteRoute(id int64) error {
	_, err := s.db.Exec(`DELETE FROM routes WHERE id=?`, id)
	return err
}

func (s *Store) SetRouteHealth(id int64, health string) error {
	_, err := s.db.Exec(`UPDATE routes SET health=?, updated_at=? WHERE id=?`, health, now(), id)
	return err
}

// ---------- Users ----------

func (s *Store) CreateUser(u *model.User) error {
	ts := now()
	if u.SubToken == "" {
		u.SubToken = strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	if u.ProxyPassword == "" {
		u.ProxyPassword = strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	}
	if u.Status == "" {
		u.Status = model.StatusActive
	}
	var exp any
	if u.ExpireAt != nil {
		exp = u.ExpireAt.UTC().Format(time.RFC3339)
	}
if u.DisplayMultiplier <= 0 {
			u.DisplayMultiplier = 1
		}
		res, err := s.db.Exec(`INSERT INTO users(username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,entry_host,entry_port,note,display_multiplier,created_at,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			u.Username, mustHash(u.ProxyPassword), u.ProxyPassword, u.Status, exp,
			u.TrafficLimitBytes, u.TrafficUsedBytes, u.SpeedLimitBps, u.MaxSessions, u.StickyExitID, u.SubToken, u.RouteID, u.EntryHost, u.EntryPort, u.Note, u.DisplayMultiplier, ts, ts)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	u.ID = id
	return nil
}

func (s *Store) UpdateUser(u *model.User) error {
	ts := now()
	var exp any
	if u.ExpireAt != nil {
		exp = u.ExpireAt.UTC().Format(time.RFC3339)
	}
	_, err := s.db.Exec(`UPDATE users SET status=?, expire_at=?, traffic_limit_bytes=?, speed_limit_bps=?, max_sessions=?, sticky_exit_id=?, route_id=?, entry_host=?, entry_port=?, note=?, updated_at=? WHERE id=?`,
		u.Status, exp, u.TrafficLimitBytes, u.SpeedLimitBps, u.MaxSessions, u.StickyExitID, u.RouteID, u.EntryHost, u.EntryPort, u.Note, ts, u.ID)
	return err
}

func (s *Store) ResetUserProxyPassword(id int64) (string, error) {
	pw := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	ts := now()
	_, err := s.db.Exec(`UPDATE users SET proxy_password=?, password_hash=?, updated_at=? WHERE id=?`, pw, mustHash(pw), ts, id)
	return pw, err
}

func (s *Store) ResetSubToken(id int64) (string, error) {
		tok := strings.ReplaceAll(uuid.NewString(), "-", "")
		ts := now()
		_, err := s.db.Exec(`UPDATE users SET sub_token=?, updated_at=? WHERE id=?`, tok, ts, id)
		return tok, err
	}

	// SetUserDisplayMultiplier sets public query-page display scale (real metering unchanged).
	// mult <= 0 is stored as 1.
	func (s *Store) SetUserDisplayMultiplier(id int64, mult float64) error {
		if mult <= 0 {
			mult = 1
		}
		if mult < 0.1 {
			mult = 0.1
		}
		if mult > 100 {
			mult = 100
		}
		_, err := s.db.Exec(`UPDATE users SET display_multiplier=?, updated_at=? WHERE id=?`, mult, now(), id)
		return err
	}

	const userSelectCols = `id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,entry_host,entry_port,note,display_multiplier,created_at,updated_at`

	func (s *Store) GetUser(id int64) (*model.User, error) {
		row := s.db.QueryRow(`SELECT `+userSelectCols+` FROM users WHERE id=?`, id)
		return scanUser(row)
	}

	func (s *Store) GetUserByUsername(username string) (*model.User, error) {
		row := s.db.QueryRow(`SELECT `+userSelectCols+` FROM users WHERE username=?`, username)
		return scanUser(row)
	}

	func (s *Store) GetUserBySubToken(token string) (*model.User, error) {
		row := s.db.QueryRow(`SELECT `+userSelectCols+` FROM users WHERE sub_token=?`, token)
		return scanUser(row)
	}

	func (s *Store) ListUsers() ([]model.User, error) {
		rows, err := s.db.Query(`SELECT ` + userSelectCols + ` FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		u.ProxyPassword = "" // hide in list
		out = append(out, *u)
	}
	return out, nil
}

func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

func (s *Store) AddTraffic(userID int64, exitNodeID string, up, down int64) error {
	if up == 0 && down == 0 {
		return nil
	}
	ts := now()
	hour := time.Now().UTC().Truncate(time.Hour).Format(time.RFC3339)
	_, err := s.db.Exec(`UPDATE users SET traffic_used_bytes = traffic_used_bytes + ?, updated_at=? WHERE id=?`, up+down, ts, userID)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO traffic_hourly(user_id, exit_node_id, hour, up_bytes, down_bytes) VALUES(?,?,?,?,?)
		ON CONFLICT(user_id, exit_node_id, hour) DO UPDATE SET up_bytes = up_bytes + excluded.up_bytes, down_bytes = down_bytes + excluded.down_bytes`,
		userID, exitNodeID, hour, up, down)
	return err
}

func (s *Store) SetUserStatus(id int64, status string) error {
	_, err := s.db.Exec(`UPDATE users SET status=?, updated_at=? WHERE id=?`, status, now(), id)
	return err
}

func (s *Store) RefreshUserStatuses() error {
	_, err := s.RefreshUserStatusesChanged()
	return err
}

// RefreshUserStatusesChanged applies expire/over-quota transitions and returns
// how many active users flipped (so callers can trigger config rebuild).
func (s *Store) RefreshUserStatusesChanged() (int64, error) {
	var changed int64
	// expire
	res, err := s.db.Exec(`UPDATE users SET status=?, updated_at=? WHERE status=? AND expire_at IS NOT NULL AND expire_at < ?`,
		model.StatusExpired, now(), model.StatusActive, now())
	if err != nil {
		return 0, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		changed += n
	}
	// over quota (limit > 0)
	res, err = s.db.Exec(`UPDATE users SET status=?, updated_at=? WHERE status=? AND traffic_limit_bytes > 0 AND traffic_used_bytes >= traffic_limit_bytes`,
		model.StatusOverQuota, now(), model.StatusActive)
	if err != nil {
		return changed, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		changed += n
	}
	return changed, nil
}

func (s *Store) ListActiveProxyUsers() ([]model.User, error) {
	if err := s.RefreshUserStatuses(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT `+userSelectCols+` FROM users WHERE status=?`, model.StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, nil
}

func scanUser(row scannable) (*model.User, error) {
		var u model.User
		var exp, created, updated sql.NullString
		var routeID sql.NullInt64
		var entryHost sql.NullString
		var entryPort sql.NullInt64
		var displayMult sql.NullFloat64
		if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.ProxyPassword, &u.Status, &exp, &u.TrafficLimitBytes, &u.TrafficUsedBytes, &u.SpeedLimitBps, &u.MaxSessions, &u.StickyExitID, &u.SubToken, &routeID, &entryHost, &entryPort, &u.Note, &displayMult, &created, &updated); err != nil {
			return nil, err
		}
		if exp.Valid {
			u.ExpireAt = parseTime(exp.String)
		}
		if routeID.Valid {
			u.RouteID = &routeID.Int64
		}
		if entryHost.Valid {
			u.EntryHost = entryHost.String
		}
		if entryPort.Valid {
			u.EntryPort = int(entryPort.Int64)
		}
		if displayMult.Valid && displayMult.Float64 > 0 {
			u.DisplayMultiplier = displayMult.Float64
		} else {
			u.DisplayMultiplier = 1
		}
		if created.Valid {
			if t := parseTime(created.String); t != nil {
				u.CreatedAt = *t
			}
		}
		if updated.Valid {
			if t := parseTime(updated.String); t != nil {
				u.UpdatedAt = *t
			}
		}
		return &u, nil
	}

// ---------- Rates (memory) ----------

func (s *Store) SetRate(sample model.TrafficSample) {
	s.mu.Lock()
	s.rates[sample.UserID] = sample
	s.mu.Unlock()
}

func (s *Store) GetRate(userID int64) (model.TrafficSample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.rates[userID]
	return v, ok
}

func (s *Store) AllRates() []model.TrafficSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.TrafficSample, 0, len(s.rates))
	for _, v := range s.rates {
		out = append(out, v)
	}
	return out
}

// ---------- Desired config ----------

func (s *Store) SaveDesiredConfig(nodeID string, version int64, configJSON string) error {
	_, err := s.db.Exec(`INSERT INTO node_desired_config(node_id, version, config_json, updated_at) VALUES(?,?,?,?)
		ON CONFLICT(node_id) DO UPDATE SET version=excluded.version, config_json=excluded.config_json, updated_at=excluded.updated_at`,
		nodeID, version, configJSON, now())
	return err
}

func (s *Store) GetDesiredConfig(nodeID string) (version int64, configJSON string, err error) {
	err = s.db.QueryRow(`SELECT version, config_json FROM node_desired_config WHERE node_id=?`, nodeID).Scan(&version, &configJSON)
	return
}

// ---------- Announcements ----------

func scanAnnouncement(sc interface {
	Scan(dest ...any) error
}) (model.Announcement, error) {
	var a model.Announcement
	var en, pop int
	var c, u string
	if err := sc.Scan(&a.ID, &a.Title, &a.Body, &en, &pop, &c, &u); err != nil {
		return a, err
	}
	a.Enabled = en != 0
	a.Popup = pop != 0
	if t := parseTime(c); t != nil {
		a.CreatedAt = *t
	}
	if t := parseTime(u); t != nil {
		a.UpdatedAt = *t
	}
	return a, nil
}

func (s *Store) ListAnnouncements() ([]model.Announcement, error) {
	rows, err := s.db.Query(`SELECT id, title, body, enabled, popup, created_at, updated_at FROM announcements ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Announcement, 0)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListPublicAnnouncements returns enabled announcements for the query page (newest first).
func (s *Store) ListPublicAnnouncements() ([]model.Announcement, error) {
	rows, err := s.db.Query(`SELECT id, title, body, enabled, popup, created_at, updated_at FROM announcements WHERE enabled=1 ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Announcement, 0)
	for rows.Next() {
		a, err := scanAnnouncement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PopupAnnouncement returns the single enabled popup announcement, if any.
func (s *Store) PopupAnnouncement() (*model.Announcement, error) {
	row := s.db.QueryRow(`SELECT id, title, body, enabled, popup, created_at, updated_at FROM announcements WHERE enabled=1 AND popup=1 ORDER BY id DESC LIMIT 1`)
	a, err := scanAnnouncement(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) GetAnnouncement(id int64) (*model.Announcement, error) {
	row := s.db.QueryRow(`SELECT id, title, body, enabled, popup, created_at, updated_at FROM announcements WHERE id=?`, id)
	a, err := scanAnnouncement(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *Store) CreateAnnouncement(a *model.Announcement) error {
	ts := now()
	a.Title = strings.TrimSpace(a.Title)
	a.Body = strings.TrimSpace(a.Body)
	if a.Title == "" {
		return fmt.Errorf("标题不能为空")
	}
	if a.Body == "" {
		return fmt.Errorf("内容不能为空")
	}
	if a.Popup {
		if _, err := s.db.Exec(`UPDATE announcements SET popup=0 WHERE popup=1`); err != nil {
			return err
		}
	}
	res, err := s.db.Exec(
		`INSERT INTO announcements(title, body, enabled, popup, created_at, updated_at) VALUES(?,?,?,?,?,?)`,
		a.Title, a.Body, boolToInt(a.Enabled), boolToInt(a.Popup), ts, ts,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	a.ID = id
	if t := parseTime(ts); t != nil {
		a.CreatedAt = *t
		a.UpdatedAt = *t
	}
	return nil
}

func (s *Store) UpdateAnnouncement(a *model.Announcement) error {
	a.Title = strings.TrimSpace(a.Title)
	a.Body = strings.TrimSpace(a.Body)
	if a.Title == "" {
		return fmt.Errorf("标题不能为空")
	}
	if a.Body == "" {
		return fmt.Errorf("内容不能为空")
	}
	ts := now()
	if a.Popup {
		if _, err := s.db.Exec(`UPDATE announcements SET popup=0 WHERE popup=1 AND id<>?`, a.ID); err != nil {
			return err
		}
	}
	res, err := s.db.Exec(
		`UPDATE announcements SET title=?, body=?, enabled=?, popup=?, updated_at=? WHERE id=?`,
		a.Title, a.Body, boolToInt(a.Enabled), boolToInt(a.Popup), ts, a.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("公告不存在")
	}
	if t := parseTime(ts); t != nil {
		a.UpdatedAt = *t
	}
	return nil
}

func (s *Store) DeleteAnnouncement(id int64) error {
	_, err := s.db.Exec(`DELETE FROM announcements WHERE id=?`, id)
	return err
}

func (s *Store) SetAnnouncementPopup(id int64, popup bool) error {
	if popup {
		if _, err := s.db.Exec(`UPDATE announcements SET popup=0 WHERE popup=1`); err != nil {
			return err
		}
	}
	ts := now()
	res, err := s.db.Exec(`UPDATE announcements SET popup=?, updated_at=? WHERE id=?`, boolToInt(popup), ts, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("公告不存在")
	}
	return nil
}

// ---------- Audit & dashboard ----------

func (s *Store) Audit(actor, action, target, detail string) {
	_, _ = s.db.Exec(`INSERT INTO audit_logs(actor,action,target,detail,created_at) VALUES(?,?,?,?,?)`,
		actor, action, target, detail, now())
}

func (s *Store) ListAudit(limit int) ([]model.AuditLog, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,actor,action,target,detail,created_at FROM audit_logs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.AuditLog, 0)
	for rows.Next() {
		var a model.AuditLog
		var c string
		if err := rows.Scan(&a.ID, &a.Actor, &a.Action, &a.Target, &a.Detail, &c); err != nil {
			return nil, err
		}
		if t := parseTime(c); t != nil {
			a.CreatedAt = *t
		}
		out = append(out, a)
	}
	return out, nil
}

func (s *Store) Dashboard() (model.DashboardStats, error) {
	var st model.DashboardStats
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM nodes`).Scan(&st.TotalNodes)
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM nodes WHERE status=?`, model.StatusOnline).Scan(&st.OnlineNodes)
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM nodes WHERE status IN (?,?)`, model.StatusOffline, model.StatusDegraded).Scan(&st.UnhealthyNodes)
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM users`).Scan(&st.TotalUsers)
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM users WHERE status=?`, model.StatusActive).Scan(&st.ActiveUsers)
	day := time.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(up_bytes),0), COALESCE(SUM(down_bytes),0) FROM traffic_hourly WHERE hour >= ?`, day).Scan(&st.TodayUp, &st.TodayDown)
	return st, nil
}

func (s *Store) TodayTrafficByUser(userID int64) (up, down int64) {
	day := time.Now().UTC().Truncate(24 * time.Hour).Format(time.RFC3339)
	_ = s.db.QueryRow(`SELECT COALESCE(SUM(up_bytes),0), COALESCE(SUM(down_bytes),0) FROM traffic_hourly WHERE user_id=? AND hour >= ?`, userID, day).Scan(&up, &down)
	return
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
