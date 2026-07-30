package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/cheesydui-cloud/mieru/internal/model"
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
  hostname TEXT DEFAULT '',
  alt_hostnames TEXT DEFAULT '',
  agent_token TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'offline',
  last_seen TEXT,
  config_version INTEGER NOT NULL DEFAULT 1,
  meta_json TEXT DEFAULT '{}',
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
  note TEXT DEFAULT '',
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
`
	_, err := s.db.Exec(schema)
	return err
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
	_, err := s.db.Exec(`INSERT INTO nodes(id,name,role,region,tags,public_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		n.ID, n.Name, n.Role, n.Region, n.Tags, n.PublicIP, n.Hostname, n.AltHostnames, n.AgentToken,
		n.Status, nil, n.ConfigVersion, n.MetaJSON, ts, ts)
	if err != nil {
		return err
	}
	n.CreatedAt, _ = time.Parse(time.RFC3339, ts)
	n.UpdatedAt = n.CreatedAt
	return nil
}

func (s *Store) UpdateNode(n *model.Node) error {
	ts := now()
	_, err := s.db.Exec(`UPDATE nodes SET name=?, role=?, region=?, tags=?, public_ip=?, hostname=?, alt_hostnames=?, status=?, meta_json=?, updated_at=? WHERE id=?`,
		n.Name, n.Role, n.Region, n.Tags, n.PublicIP, n.Hostname, n.AltHostnames, n.Status, n.MetaJSON, ts, n.ID)
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
	row := s.db.QueryRow(`SELECT id,name,role,region,tags,public_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at FROM nodes WHERE id=?`, id)
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
	rows, err := s.db.Query(`SELECT id,name,role,region,tags,public_ip,hostname,alt_hostnames,agent_token,status,last_seen,config_version,meta_json,created_at,updated_at FROM nodes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Node
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

func (s *Store) DeleteNode(id string) error {
	_, err := s.db.Exec(`DELETE FROM nodes WHERE id=?`, id)
	return err
}

func (s *Store) Heartbeat(nodeID string, publicIP, hostname string, status string) error {
	ts := now()
	_, err := s.db.Exec(`UPDATE nodes SET last_seen=?, status=?, public_ip=CASE WHEN ?!='' THEN ? ELSE public_ip END, hostname=CASE WHEN ?!='' THEN ? ELSE hostname END, updated_at=? WHERE id=?`,
		ts, status, publicIP, publicIP, hostname, hostname, ts, nodeID)
	return err
}

type scannable interface {
	Scan(dest ...any) error
}

func scanNode(row scannable) (*model.Node, error) {
	var n model.Node
	var lastSeen, created, updated sql.NullString
	var meta sql.NullString
	if err := row.Scan(&n.ID, &n.Name, &n.Role, &n.Region, &n.Tags, &n.PublicIP, &n.Hostname, &n.AltHostnames, &n.AgentToken, &n.Status, &lastSeen, &n.ConfigVersion, &meta, &created, &updated); err != nil {
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
	var out []model.Route
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
	res, err := s.db.Exec(`INSERT INTO users(username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Username, mustHash(u.ProxyPassword), u.ProxyPassword, u.Status, exp,
		u.TrafficLimitBytes, u.TrafficUsedBytes, u.SpeedLimitBps, u.MaxSessions, u.StickyExitID, u.SubToken, u.RouteID, u.Note, ts, ts)
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
	_, err := s.db.Exec(`UPDATE users SET status=?, expire_at=?, traffic_limit_bytes=?, speed_limit_bps=?, max_sessions=?, sticky_exit_id=?, route_id=?, note=?, updated_at=? WHERE id=?`,
		u.Status, exp, u.TrafficLimitBytes, u.SpeedLimitBps, u.MaxSessions, u.StickyExitID, u.RouteID, u.Note, ts, u.ID)
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

func (s *Store) GetUser(id int64) (*model.User, error) {
	row := s.db.QueryRow(`SELECT id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at FROM users WHERE id=?`, id)
	return scanUser(row)
}

func (s *Store) GetUserByUsername(username string) (*model.User, error) {
	row := s.db.QueryRow(`SELECT id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at FROM users WHERE username=?`, username)
	return scanUser(row)
}

func (s *Store) GetUserBySubToken(token string) (*model.User, error) {
	row := s.db.QueryRow(`SELECT id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at FROM users WHERE sub_token=?`, token)
	return scanUser(row)
}

func (s *Store) ListUsers() ([]model.User, error) {
	rows, err := s.db.Query(`SELECT id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at FROM users ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
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
	// expire
	_, err := s.db.Exec(`UPDATE users SET status=?, updated_at=? WHERE status=? AND expire_at IS NOT NULL AND expire_at < ?`,
		model.StatusExpired, now(), model.StatusActive, now())
	if err != nil {
		return err
	}
	// over quota (limit > 0)
	_, err = s.db.Exec(`UPDATE users SET status=?, updated_at=? WHERE status=? AND traffic_limit_bytes > 0 AND traffic_used_bytes >= traffic_limit_bytes`,
		model.StatusOverQuota, now(), model.StatusActive)
	return err
}

func (s *Store) ListActiveProxyUsers() ([]model.User, error) {
	if err := s.RefreshUserStatuses(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id,username,password_hash,proxy_password,status,expire_at,traffic_limit_bytes,traffic_used_bytes,speed_limit_bps,max_sessions,sticky_exit_id,sub_token,route_id,note,created_at,updated_at FROM users WHERE status=?`, model.StatusActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.User
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
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.ProxyPassword, &u.Status, &exp, &u.TrafficLimitBytes, &u.TrafficUsedBytes, &u.SpeedLimitBps, &u.MaxSessions, &u.StickyExitID, &u.SubToken, &routeID, &u.Note, &created, &updated); err != nil {
		return nil, err
	}
	if exp.Valid {
		u.ExpireAt = parseTime(exp.String)
	}
	if routeID.Valid {
		u.RouteID = &routeID.Int64
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
	var out []model.AuditLog
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
