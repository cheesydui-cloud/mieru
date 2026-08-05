package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/auth"
	"github.com/cheesydui-cloud/mieru/internal/cloudflare"
	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/configgen"
	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
	"github.com/cheesydui-cloud/mieru/web"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// dialJob is a one-shot TCP probe the panel asks an agent to perform.
type dialJob struct {
	ID      string `json:"id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Timeout int    `json:"timeout_ms"` // client-side dial timeout hint
}

type dialResult struct {
	OK      bool
	Latency int64
	Error   string
}

// upgradeJob is delivered on heartbeat; agent downloads release tarball and restarts.
type upgradeJob struct {
	ID      string   `json:"id"`
	Version string   `json:"version"` // e.g. v0.4.6
	URLs    []string `json:"urls"`    // full tarball URLs (github + mirrors)
	Asset   string   `json:"asset"`   // filename hint
}

// panelURLJob is delivered on heartbeat; agent rewrites AGENT_PANEL_URL and restarts.
type panelURLJob struct {
	ID  string `json:"id"`
	URL string `json:"url"` // normalized panel base URL, no trailing slash
}

type Server struct {
	cfg     config.PanelConfig
	store   *store.Store
	jwt     *auth.TokenManager
	gen     *configgen.Builder
	Version string

	// pending dials: nodeID → jobID → waiting channel
	dialMu   sync.Mutex
	dialWait map[string]map[string]chan dialResult // nodeID -> jobID -> ch
	dialJobs map[string][]dialJob                  // nodeID -> queued jobs

	// pending agent self-upgrades (one per node; replaced if re-queued)
	upgradeMu   sync.Mutex
	upgradeJobs map[string]*upgradeJob // nodeID -> job

	// pending panel URL rewrites (one per node; replaced if re-queued)
	panelURLMu   sync.Mutex
	panelURLJobs map[string]*panelURLJob // nodeID -> job

	// chunked client-file uploads (bypass reverse-proxy body limits)
	uploadMu      sync.Mutex
	pendingUpload map[string]*pendingClientUpload // uploadID -> session

	// login rate limit (per IP)
	loginMu   sync.Mutex
	loginFail map[string]*loginAttempt

	// last successful traffic report per exit node (for metering trust UI)
	trafficMu   sync.Mutex
	lastTraffic map[string]time.Time // nodeID -> when

	// last probe result per route (for list UI)
	probeMu   sync.Mutex
	lastProbe map[int64]probeSnap // routeID

	// serialize background auto-rebuilds (startup / quota flip)
	rebuildMu         sync.Mutex
	rebuildBusy       bool
	lastRebuildAt     time.Time
	lastRebuildOK     bool
	lastRebuildReason string
	lastRebuildErr    string
}

// OfflineAfter is how long without heartbeat before a node is treated as offline.
const OfflineAfter = 60 * time.Second

type loginAttempt struct {
	fails     int
	windowAt  time.Time
	lockUntil time.Time
}

type probeSnap struct {
	At     time.Time
	Health string
}

// pendingClientUpload is a multi-chunk admin upload session (stored under DataDir/client-files/tmp).
type pendingClientUpload struct {
	ID          string
	Filename    string
	Title       string
	ContentType string
	Enabled     bool
	Size        int64
	Received    int64
	Path        string
	CreatedAt   time.Time
}

func New(cfg config.PanelConfig, st *store.Store) *Server {
	return &Server{
		cfg:           cfg,
		store:         st,
		jwt:           auth.NewTokenManager(cfg.JWTSecret),
		gen:           &configgen.Builder{Store: st},
		Version:       "dev",
		dialWait:      map[string]map[string]chan dialResult{},
		dialJobs:      map[string][]dialJob{},
		upgradeJobs:   map[string]*upgradeJob{},
		panelURLJobs:  map[string]*panelURLJob{},
		pendingUpload: map[string]*pendingClientUpload{},
		loginFail:     map[string]*loginAttempt{},
		lastTraffic:   map[string]time.Time{},
		lastProbe:     map[int64]probeSnap{},
	}
}

// EnsureDesiredConfigs rebuilds all node desired configs once at panel boot so
// agents get a fresh version after panel upgrade without manual「重建配置」.
// Safe to call concurrent with HTTP — uses single-flight.
func (s *Server) EnsureDesiredConfigs() {
	s.scheduleRebuild("startup")
}

// scheduleRebuild runs RebuildAll in the background (single-flight).
// reason is only for logs/audit detail.
func (s *Server) scheduleRebuild(reason string) {
	s.rebuildMu.Lock()
	if s.rebuildBusy {
		s.rebuildMu.Unlock()
		return
	}
	s.rebuildBusy = true
	s.rebuildMu.Unlock()
	go func() {
		err := s.gen.RebuildAll()
		s.rebuildMu.Lock()
		s.rebuildBusy = false
		s.lastRebuildAt = time.Now()
		s.lastRebuildReason = reason
		if err != nil {
			s.lastRebuildOK = false
			s.lastRebuildErr = err.Error()
			s.rebuildMu.Unlock()
			log.Printf("auto-rebuild (%s) failed: %v", reason, err)
			s.store.Audit("system", "auto_rebuild_fail", "*", reason+": "+err.Error())
			return
		}
		s.lastRebuildOK = true
		s.lastRebuildErr = ""
		s.rebuildMu.Unlock()
		log.Printf("auto-rebuild (%s) ok", reason)
		s.store.Audit("system", "auto_rebuild", "*", reason)
	}()
}

// rebuildNow runs RebuildAll synchronously and records status (for admin API).
func (s *Server) rebuildNow(reason string) error {
	err := s.gen.RebuildAll()
	s.rebuildMu.Lock()
	s.lastRebuildAt = time.Now()
	s.lastRebuildReason = reason
	if err != nil {
		s.lastRebuildOK = false
		s.lastRebuildErr = err.Error()
	} else {
		s.lastRebuildOK = true
		s.lastRebuildErr = ""
	}
	s.rebuildMu.Unlock()
	return err
}

func (s *Server) rebuildStatusSnapshot() gin.H {
	s.rebuildMu.Lock()
	defer s.rebuildMu.Unlock()
	out := gin.H{
		"busy":   s.rebuildBusy,
		"ok":     s.lastRebuildOK,
		"reason": s.lastRebuildReason,
		"error":  s.lastRebuildErr,
	}
	if !s.lastRebuildAt.IsZero() {
		out["at"] = s.lastRebuildAt.UTC().Format(time.RFC3339)
		out["age_sec"] = int64(time.Since(s.lastRebuildAt).Seconds())
	}
	return out
}

// applyOfflineStatus flips nodes with stale heartbeat to offline for API responses.
func applyOfflineStatus(n *model.Node, now time.Time) {
	if n == nil {
		return
	}
	if n.LastSeen == nil {
		if n.Status == model.StatusOnline || n.Status == model.StatusDegraded {
			n.Status = model.StatusOffline
		}
		return
	}
	if now.Sub(*n.LastSeen) > OfflineAfter {
		if n.Status == model.StatusOnline || n.Status == model.StatusDegraded {
			n.Status = model.StatusOffline
		}
	}
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	// Default is 32 MiB; keep headroom for single-shot multipart uploads.
	r.MaxMultipartMemory = 64 << 20
	r.Use(gin.Recovery(), gin.Logger())

	c := cors.Config{
		AllowMethods:  []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:  []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders: []string{"Content-Length"},
		MaxAge:        12 * time.Hour,
	}
	// Browsers forbid AllowCredentials + AllowAllOrigins(*). Same-origin SPA needs neither.
	if len(s.cfg.CORSOrigins) == 1 && s.cfg.CORSOrigins[0] == "*" {
		c.AllowAllOrigins = true
		c.AllowCredentials = false
	} else {
		c.AllowOrigins = s.cfg.CORSOrigins
		c.AllowCredentials = true
	}
	r.Use(cors.New(c))

	r.GET("/api/health", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.JSON(http.StatusOK, gin.H{"ok": true, "ts": time.Now().UTC(), "version": s.Version})
	})
	r.GET("/api/version", func(c *gin.Context) {
		// Prevent CDN/browser from sticky-caching an old panel version in the sidebar.
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.JSON(http.StatusOK, gin.H{"version": s.Version, "ui": "embedded"})
	})
	// Public brand (login page + tab title/favicon) — no auth.
	r.GET("/api/brand", s.publicBrand)

	r.GET("/sub/:token", s.subscription)
	r.GET("/api/sub/:token", s.subscription)
	// Mihomo / Clash Meta: import as remote config or download file
	r.GET("/sub/:token/mihomo.yaml", s.subscriptionMihomo)
	r.GET("/api/sub/:token/mihomo.yaml", s.subscriptionMihomo)
	// Public user info page (read-only; no password) — shareable link from admin「更多」
	r.GET("/api/u/:token", s.publicUserInfo)
	// Public announcements for user query page (no auth)
	r.GET("/api/announcements", s.publicAnnouncements)
	// Public client files for query page (no auth; global list)
	r.GET("/api/files", s.publicClientFiles)
	r.GET("/api/files/:id/download", s.publicDownloadClientFile)
	r.POST("/api/auth/login", s.login)

	agent := r.Group("/api/agent")
	{
		agent.POST("/heartbeat", s.agentHeartbeat)
		agent.GET("/config", s.agentConfig)
		agent.POST("/traffic", s.agentTraffic)
		agent.POST("/dial-result", s.agentDialResult)
		agent.POST("/upgrade-result", s.agentUpgradeResult)
		agent.POST("/panel-url-result", s.agentPanelURLResult)
	}

	admin := r.Group("/api/admin")
	admin.Use(s.requireAdmin())
	{
		admin.GET("/dashboard", s.dashboard)
		admin.GET("/nodes", s.listNodes)
		admin.POST("/nodes", s.createNode)
		admin.GET("/nodes/:id", s.getNode)
		admin.PUT("/nodes/:id", s.updateNode)
		admin.DELETE("/nodes/:id", s.deleteNode)
		admin.POST("/nodes/:id/rebuild", s.rebuildAll)
		admin.POST("/nodes/:id/upgrade", s.upgradeNode)
		admin.POST("/nodes/upgrade-all", s.upgradeAllNodes)
		admin.POST("/nodes/:id/sync-panel-url", s.syncNodePanelURL)
		admin.POST("/nodes/sync-panel-url", s.syncAllPanelURL)
		admin.POST("/rebuild", s.rebuildAll)

		admin.GET("/routes", s.listRoutes)
		admin.POST("/routes", s.createRoute)
		admin.PUT("/routes/:id", s.updateRoute)
		admin.DELETE("/routes/:id", s.deleteRoute)
		admin.POST("/routes/:id/probe", s.probeRoute)

		admin.GET("/users", s.listUsers)
		admin.POST("/users", s.createUser)
		admin.GET("/users/:id", s.getUser)
		admin.GET("/users/:id/share", s.getUserShare)
		admin.GET("/users/:id/mihomo.yaml", s.getUserMihomoYAML)
		admin.PUT("/users/:id", s.updateUser)
		admin.DELETE("/users/:id", s.deleteUser)
		admin.POST("/users/:id/reset-password", s.resetUserPassword)
		admin.POST("/users/:id/reset-sub", s.resetUserSub)
		admin.POST("/users/:id/renew", s.renewUser)
		admin.POST("/users/:id/add-traffic", s.addUserTraffic)
		admin.POST("/users/:id/toggle", s.toggleUser)
		admin.POST("/users/:id/display-multiplier", s.setUserDisplayMultiplier)
		admin.POST("/users/:id/speed-limit", s.setUserSpeedLimit)
		admin.POST("/users/batch", s.batchUsers)

		admin.GET("/metrics/rates", s.listRates)
		admin.GET("/audit", s.listAudit)
		admin.GET("/rebuild-status", s.getRebuildStatus)
		admin.GET("/backup", s.exportBackup)
		admin.GET("/migration/export", s.exportMigration)
		admin.POST("/migration/import", s.importMigration)

		admin.GET("/settings", s.getSettings)
		admin.PUT("/settings", s.putSettings)
		admin.PUT("/user-info-locale", s.putUserInfoLocale)
		admin.POST("/admin-password", s.changeAdminPassword)
		admin.POST("/cloudflare/dns", s.cloudflareUpsertDNS)
		admin.GET("/cloudflare/lookup", s.cloudflareLookupDNS)
		admin.POST("/cloudflare/test", s.cloudflareTest)
		admin.GET("/nodes/:id/install", s.nodeInstallCmd)
		admin.GET("/diagnose", s.diagnose)
		admin.GET("/nodes/:id/desired", s.nodeDesiredConfig)
		admin.GET("/announcements", s.listAnnouncements)
		admin.POST("/announcements", s.createAnnouncement)
		admin.PUT("/announcements/:id", s.updateAnnouncement)
		admin.DELETE("/announcements/:id", s.deleteAnnouncement)
		admin.POST("/announcements/:id/popup", s.setAnnouncementPopup)

		admin.GET("/files", s.listClientFiles)
		admin.POST("/files", s.uploadClientFile)
		// Chunked upload (preferred): survives nginx default client_max_body_size 1m
		admin.POST("/files/upload/init", s.initClientFileUpload)
		admin.PUT("/files/upload/:id/chunk", s.chunkClientFileUpload)
		admin.POST("/files/upload/:id/complete", s.completeClientFileUpload)
		admin.DELETE("/files/upload/:id", s.abortClientFileUpload)
		admin.PUT("/files/:id", s.updateClientFile)
		admin.DELETE("/files/:id", s.deleteClientFile)
	}

	portal := r.Group("/api/me")
	portal.Use(s.requireUserOrAdmin())
	{
		portal.GET("/profile", s.myProfile)
		portal.GET("/rates", s.myRate)
	}

	// SPA frontend: embedded dist first, on-disk ./web/dist fallback
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		dist = web.Dist
	}
	// Explicit routes so "/" never falls into Gin's default 404
	serveIndex := func(c *gin.Context) {
		if !serveStatic(c, dist, "index.html") {
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(fallbackHTML))
		}
	}
	r.GET("/", serveIndex)
	r.GET("/index.html", serveIndex)
	r.HEAD("/", serveIndex)
	r.GET("/assets/*filepath", func(c *gin.Context) {
		rel := path.Join("assets", strings.TrimPrefix(c.Param("filepath"), "/"))
		if !serveStatic(c, dist, rel) {
			c.Status(http.StatusNotFound)
			c.Header("Content-Type", "text/plain; charset=utf-8")
			_, _ = c.Writer.Write([]byte("asset not found\n"))
		}
	})
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		if strings.HasPrefix(p, "/api") || strings.HasPrefix(p, "/sub/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		// try exact static file
		rel := strings.TrimPrefix(p, "/")
		if rel != "" && !strings.Contains(rel, "..") {
			if serveStatic(c, dist, rel) {
				return
			}
		}
		// Vue router history mode → index.html
		serveIndex(c)
	})

	return r
}

const fallbackHTML = `<!doctype html><html><head><meta charset="utf-8"><title>Mieru Panel</title>
<style>body{font-family:system-ui;background:#0b0f14;color:#e8eef6;display:grid;place-items:center;min-height:100vh;margin:0}
main{max-width:520px;padding:32px;border:1px solid #243041;border-radius:16px;background:#121820}
code{background:#1a222d;padding:2px 6px;border-radius:6px}</style></head><body><main>
<h1>Mieru Panel</h1><p>API is running, but UI assets are missing.</p>
<p>Upgrade to a release with embedded UI, or place <code>web/dist</code> next to the binary.</p>
<p>Health: <a href="/api/health" style="color:#5b8cff">/api/health</a></p>
</main></body></html>`

func serveStatic(c *gin.Context, dist fs.FS, rel string) bool {
	// index.html must not be cached — otherwise browsers keep old asset hashes after upgrade
	if rel == "index.html" || strings.HasSuffix(rel, "/index.html") {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
	} else if strings.HasPrefix(rel, "assets/") {
		// hashed assets are immutable
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	// 1) embedded only (never prefer on-disk over embed in production)
	if f, err := dist.Open(rel); err == nil {
		defer f.Close()
		stat, err := f.Stat()
		if err == nil && !stat.IsDir() {
			data, err := io.ReadAll(f)
			if err == nil {
				c.Data(http.StatusOK, contentType(rel), data)
				return true
			}
		}
	}
	// 2) on-disk fallback for local dev only when embed miss
	disk := path.Join("web", "dist", rel)
	if st, err := os.Stat(disk); err == nil && !st.IsDir() {
		c.File(disk)
		return true
	}
	return false
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".html"):
		return "text/html; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "application/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".json"):
		return "application/json"
	default:
		return "application/octet-stream"
	}
}

func (s *Server) requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := s.bearer(c)
		if err != nil || claims.Role != "admin" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}

func (s *Server) requireUserOrAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, err := s.bearer(c)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
			return
		}
		c.Set("claims", claims)
		c.Next()
	}
}

func (s *Server) bearer(c *gin.Context) (*auth.Claims, error) {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, fmt.Errorf("missing bearer")
	}
	return s.jwt.Parse(strings.TrimPrefix(h, "Bearer "))
}

func (s *Server) clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

func (s *Server) loginAllowed(ip string) (ok bool, waitSec int) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	a := s.loginFail[ip]
	if a == nil {
		return true, 0
	}
	if now.Before(a.lockUntil) {
		return false, int(a.lockUntil.Sub(now).Seconds()) + 1
	}
	// reset window after 15 minutes quiet
	if now.Sub(a.windowAt) > 15*time.Minute {
		delete(s.loginFail, ip)
	}
	return true, 0
}

func (s *Server) loginFailRecord(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	a := s.loginFail[ip]
	if a == nil || now.Sub(a.windowAt) > 15*time.Minute {
		a = &loginAttempt{windowAt: now}
		s.loginFail[ip] = a
	}
	a.fails++
	// progressive lock: 5 fails → 60s, then +60s per fail, cap 15m
	if a.fails >= 5 {
		sec := 60 + (a.fails-5)*60
		if sec > 900 {
			sec = 900
		}
		a.lockUntil = now.Add(time.Duration(sec) * time.Second)
	}
}

func (s *Server) loginSuccessClear(ip string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginFail, ip)
}

func (s *Server) login(c *gin.Context) {
	ip := s.clientIP(c)
	if ok, wait := s.loginAllowed(ip); !ok {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":    fmt.Sprintf("登录失败次数过多，请 %d 秒后再试", wait),
			"retry_in": wait,
		})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	if req.Username == "" || req.Password == "" {
		s.loginFailRecord(ip)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if adm, err := s.store.GetAdminByUsername(req.Username); err == nil {
		if store.CheckPassword(adm.PasswordHash, req.Password) {
			tok, err := s.jwt.Issue(fmt.Sprintf("%d", adm.ID), "admin", adm.Username)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "token issue failed"})
				return
			}
			s.loginSuccessClear(ip)
			s.store.Audit(adm.Username, "login", "admin", "ok")
			c.JSON(http.StatusOK, gin.H{"token": tok, "role": "admin", "username": adm.Username})
			return
		}
	}
	if u, err := s.store.GetUserByUsername(req.Username); err == nil {
		if u.ProxyPassword == req.Password || store.CheckPassword(u.PasswordHash, req.Password) {
			tok, err := s.jwt.Issue(fmt.Sprintf("%d", u.ID), "user", u.Username)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "token issue failed"})
				return
			}
			s.loginSuccessClear(ip)
			s.store.Audit(u.Username, "login", "user", "ok")
			c.JSON(http.StatusOK, gin.H{"token": tok, "role": "user", "username": u.Username})
			return
		}
	}
	s.loginFailRecord(ip)
	s.store.Audit(req.Username, "login_fail", ip, "invalid credentials")
	c.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
}

func (s *Server) dashboard(c *gin.Context) {
	st, err := s.store.Dashboard()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, st)
}

func (s *Server) listNodes(c *gin.Context) {
	list, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type nodeOut struct {
		model.Node
		AgentToken       string `json:"agent_token,omitempty"`
		AgentVersion     string `json:"agent_version,omitempty"`
		ApplyError       string `json:"apply_error,omitempty"`
		UpgradeStatus    string `json:"upgrade_status,omitempty"` // pending|running|ok|error
		UpgradeTarget    string `json:"upgrade_target,omitempty"` // target version
		UpgradeError     string `json:"upgrade_error,omitempty"`
		UpgradePending   bool   `json:"upgrade_pending,omitempty"`  // still in panel queue
		PanelURLStatus   string `json:"panel_url_status,omitempty"` // pending|ok|error
		PanelURLTarget   string `json:"panel_url_target,omitempty"`
		PanelURLError    string `json:"panel_url_error,omitempty"`
		PanelURLPending  bool   `json:"panel_url_pending,omitempty"`
		AgentPanelURL    string `json:"agent_panel_url,omitempty"`    // last reported by agent
		PanelURLMismatch bool   `json:"panel_url_mismatch,omitempty"` // agent URL != settings
		PanelURLFixCmd   string `json:"panel_url_fix_cmd,omitempty"` // offline one-liner
		PanelVersion     string `json:"panel_version,omitempty"`
		AgentConfigVer   int64  `json:"agent_config_version,omitempty"` // last reported applied version
		ConfigStale      bool   `json:"config_stale,omitempty"`         // desired > agent applied
		HeartbeatAgeSec  int64  `json:"heartbeat_age_sec,omitempty"`
		NoHeartbeat      bool   `json:"no_heartbeat,omitempty"` // never seen or >N min
		TrafficReportAge int64  `json:"traffic_report_age_sec,omitempty"`
		TrafficReporting bool   `json:"traffic_reporting,omitempty"`
		MeteringCapable  bool   `json:"metering_capable,omitempty"` // exit/hybrid + agent >= 0.4.17
		MeteringHint     string `json:"metering_hint,omitempty"`
	}
	out := make([]nodeOut, 0, len(list))
	reveal := c.Query("reveal") == "1"
	panelVer := strings.TrimPrefix(strings.TrimSpace(s.Version), "v")
	now := time.Now()
	for _, n := range list {
		applyOfflineStatus(&n, now)
		no := nodeOut{Node: n, PanelVersion: panelVer}
		no.AgentToken = ""
		no.Node.AgentToken = ""
		// surface last apply error from meta_json for degraded nodes
		if n.MetaJSON != "" {
			var meta map[string]interface{}
			if json.Unmarshal([]byte(n.MetaJSON), &meta) == nil {
				if v, ok := meta["apply_error"].(string); ok {
					no.ApplyError = v
				}
				if v, ok := meta["agent_version"].(string); ok {
					// Normalize: strip leading "v" so UI "v{{ver}}" never becomes "vv0.3.10"
					no.AgentVersion = strings.TrimPrefix(strings.TrimSpace(v), "v")
				}
				if v, ok := meta["upgrade_status"].(string); ok {
					no.UpgradeStatus = v
				}
				if v, ok := meta["upgrade_target"].(string); ok {
					no.UpgradeTarget = strings.TrimPrefix(strings.TrimSpace(v), "v")
				}
				if v, ok := meta["upgrade_error"].(string); ok {
					no.UpgradeError = v
				}
				if v, ok := meta["panel_url_status"].(string); ok {
					no.PanelURLStatus = v
				}
				if v, ok := meta["panel_url_target"].(string); ok {
					no.PanelURLTarget = strings.TrimSpace(v)
				}
				if v, ok := meta["panel_url_error"].(string); ok {
					no.PanelURLError = v
				}
				if v, ok := meta["agent_panel_url"].(string); ok {
					no.AgentPanelURL = strings.TrimSpace(v)
				}
				// agent last applied config version (from heartbeat)
				switch cv := meta["agent_config_version"].(type) {
				case float64:
					no.AgentConfigVer = int64(cv)
				case string:
					if n, err := strconv.ParseInt(cv, 10, 64); err == nil {
						no.AgentConfigVer = n
					}
				case json.Number:
					if n, err := cv.Int64(); err == nil {
						no.AgentConfigVer = n
					}
				}
			}
		}
		if no.AgentConfigVer > 0 && n.ConfigVersion > no.AgentConfigVer {
			no.ConfigStale = true
		}
		if n.LastSeen != nil {
			age := int64(now.Sub(*n.LastSeen).Seconds())
			if age < 0 {
				age = 0
			}
			no.HeartbeatAgeSec = age
			// > OfflineAfter without heartbeat → highlight / treat offline
			if age > int64(OfflineAfter.Seconds()) {
				no.NoHeartbeat = true
			}
		} else {
			no.NoHeartbeat = true
			no.HeartbeatAgeSec = -1
		}
		// traffic metering trust for exit/hybrid
		if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
			s.trafficMu.Lock()
			if t, ok := s.lastTraffic[n.ID]; ok && !t.IsZero() {
				age := int64(now.Sub(t).Seconds())
				if age < 0 {
					age = 0
				}
				no.TrafficReportAge = age
				no.TrafficReporting = age <= 30
			} else {
				no.TrafficReportAge = -1
			}
			s.trafficMu.Unlock()
			no.MeteringCapable = agentVersionAtLeast(no.AgentVersion, 0, 4, 17)
			switch {
			case no.AgentVersion == "":
				no.MeteringHint = "无 Agent 版本（未上线）"
			case !no.MeteringCapable:
				no.MeteringHint = "需 Agent ≥ v0.4.17 才可靠计量"
			case !no.TrafficReporting:
				no.MeteringHint = "计量开启但近期无上报（检查 mita 用户名是否对齐）"
			default:
				no.MeteringHint = "计量正常"
			}
		}
		s.upgradeMu.Lock()
		if _, ok := s.upgradeJobs[n.ID]; ok {
			no.UpgradePending = true
			if no.UpgradeStatus == "" {
				no.UpgradeStatus = "pending"
			}
		}
		s.upgradeMu.Unlock()
		s.panelURLMu.Lock()
		if _, ok := s.panelURLJobs[n.ID]; ok {
			no.PanelURLPending = true
			if no.PanelURLStatus == "" {
				no.PanelURLStatus = "pending"
			}
		}
		s.panelURLMu.Unlock()
		// Detect PANEL_URL drift vs settings; offline nodes get a copy-paste fix command.
		wantPU := s.currentPanelURLSetting()
		if wantPU != "" {
			alreadyOK := no.AgentPanelURL != "" && panelURLEqual(no.AgentPanelURL, wantPU)
			if alreadyOK {
				// Agent already points at settings — don't show sticky "纠正中" badges.
				sticky := no.PanelURLPending || no.PanelURLStatus == "pending" || no.PanelURLStatus == "error"
				no.PanelURLMismatch = false
				no.PanelURLPending = false
				no.PanelURLStatus = "ok"
				no.PanelURLError = ""
				no.PanelURLTarget = wantPU
				// Drop stale in-memory job so heartbeat stops re-pushing.
				s.clearPanelURLJob(n.ID)
				if sticky {
					_ = s.store.HeartbeatEx(n.ID, "", "", "", map[string]string{
						"panel_url_status": "ok",
						"panel_url_error":  "",
						"panel_url_target": wantPU,
					})
				}
			} else if no.AgentPanelURL != "" && !panelURLEqual(no.AgentPanelURL, wantPU) {
				no.PanelURLMismatch = true
				if no.PanelURLTarget == "" {
					no.PanelURLTarget = wantPU
				}
			}
			// Offline / mismatch repair one-liner (uses stored token).
			if tok := strings.TrimSpace(n.AgentToken); tok != "" {
				role := n.Role
				if role == "" {
					role = "exit"
				}
				no.PanelURLFixCmd = fmt.Sprintf(
					"curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash -s -- --panel-url %s --node-id %s --token %s --role %s",
					wantPU, n.ID, tok, role,
				)
			}
		}
		if reveal {
			if full, err := s.store.GetNode(n.ID); err == nil {
				no.AgentToken = full.AgentToken
			}
		}
		out = append(out, no)
	}
	c.JSON(http.StatusOK, out)
}

// agentVersionAtLeast compares dotted semver (no leading v).
func agentVersionAtLeast(ver string, maj, min, patch int) bool {
	ver = strings.TrimPrefix(strings.TrimSpace(ver), "v")
	if ver == "" {
		return false
	}
	parts := strings.Split(ver, ".")
	nums := []int{0, 0, 0}
	for i := 0; i < 3 && i < len(parts); i++ {
		p := parts[i]
		// strip pre-release suffix
		if j := strings.IndexAny(p, "-+"); j >= 0 {
			p = p[:j]
		}
		n, _ := strconv.Atoi(p)
		nums[i] = n
	}
	if nums[0] != maj {
		return nums[0] > maj
	}
	if nums[1] != min {
		return nums[1] > min
	}
	return nums[2] >= patch
}

func (s *Server) createNode(c *gin.Context) {
	var req model.Node
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Name == "" || req.Role == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name and role required"})
		return
	}
	req.PrivateIP = strings.TrimSpace(req.PrivateIP)
	req.PublicIP = strings.TrimSpace(req.PublicIP)
	req.NormalizePorts()
	if err := validateNodePorts(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.MetaJSON == "" {
		req.MetaJSON = "{}"
	}
	if err := s.store.CreateNode(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "create_node", req.ID, req.Name)
	full, _ := s.store.GetNode(req.ID)
	install := s.buildInstallCmd(c, full)
	c.JSON(http.StatusCreated, gin.H{
		"node":         full,
		"agent_token":  full.AgentToken,
		"panel_url":    install.PanelURL,
		"install_cmd":  install.Cmd,
		"install_hint": install.Hint,
	})
}

func (s *Server) getNode(c *gin.Context) {
	n, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	token := n.AgentToken
	n.AgentToken = ""
	install := s.buildInstallCmd(c, &model.Node{ID: n.ID, Role: n.Role, AgentToken: token})
	c.JSON(http.StatusOK, gin.H{
		"node":        n,
		"agent_token": token,
		"panel_url":   install.PanelURL,
		"install_cmd": install.Cmd,
	})
}

func (s *Server) updateNode(c *gin.Context) {
	n, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req model.Node
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	n.Name = req.Name
	n.Role = req.Role
	n.Region = req.Region
	n.Tags = req.Tags
	n.PublicIP = req.PublicIP
	n.PrivateIP = strings.TrimSpace(req.PrivateIP)
	n.Hostname = req.Hostname
	n.AltHostnames = req.AltHostnames
	n.ListenPort = req.ListenPort
	n.PortMin = req.PortMin
	n.PortMax = req.PortMax
	n.NormalizePorts()
	if req.Status != "" {
		n.Status = req.Status
	}
	if req.MetaJSON != "" {
		n.MetaJSON = req.MetaJSON
	}
	if err := validateNodePorts(n); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.UpdateNode(n); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "update_node", n.ID, n.Name)
	n.AgentToken = ""
	c.JSON(http.StatusOK, n)
}

func validateNodePorts(n *model.Node) error {
	if n.PortMin < 0 || n.PortMin > 65535 || n.PortMax < 0 || n.PortMax > 65535 {
		return fmt.Errorf("端口范围必须在 0-65535（0=使用角色默认）")
	}
	if (n.PortMin > 0) != (n.PortMax > 0) {
		return fmt.Errorf("起始端口和结束端口需同时填写，或都填 0 使用默认")
	}
	if n.PortMin > 0 && n.PortMax > 0 && n.PortMin > n.PortMax {
		return fmt.Errorf("起始端口不能大于结束端口")
	}
	return nil
}

func (s *Server) deleteNode(c *gin.Context) {
	id := c.Param("id")
	n, err := s.store.GetNode(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	name := n.Name
	res, err := s.store.DeleteNode(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// Drop in-memory jobs so a re-created node id does not inherit them.
	s.clearUpgradeJob(id)
	s.dialMu.Lock()
	delete(s.dialJobs, id)
	delete(s.dialWait, id)
	s.dialMu.Unlock()

	// Rebuild remaining fronts/exits: free ports, drop ghost mita users.
	_ = s.gen.RebuildAll()
	detail, _ := json.Marshal(res)
	s.store.Audit("admin", "delete_node", id, string(detail))
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"name":           name,
		"routes_deleted": res.RoutesDeleted,
		"routes_updated": res.RoutesUpdated,
		"users_unbound":  res.UsersUnbound,
		"message":        "节点已删除；在线 Agent 下次心跳将 401 并停用服务",
	})
}

// agentReleaseURLs builds GitHub + CN-mirror tarball URLs for a panel release tag.
func agentReleaseURLs(version string) (asset string, urls []string) {
	ver := strings.TrimSpace(version)
	if ver == "" {
		return "", nil
	}
	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}
	// Agent is packed inside the panel release tarball (amd64/arm64 selected on agent).
	// We hand the agent the version; it picks arch-specific asset name itself.
	// URLs here are templates replaced by agent for arch — panel sends both arches.
	repo := "cheesydui-cloud/mieru"
	for _, arch := range []string{"amd64", "arm64"} {
		asset = fmt.Sprintf("mieru-panel-%s-linux-%s.tar.gz", ver, arch)
		base := fmt.Sprintf("%s/releases/download/%s/%s", repo, ver, asset)
		urls = append(urls,
			"https://github.com/"+base,
			"https://ghfast.top/https://github.com/"+base,
			"https://mirror.ghproxy.com/https://github.com/"+base,
			"https://ghproxy.net/https://github.com/"+base,
			"https://gitdl.cn/https://github.com/"+base,
		)
	}
	return asset, urls
}

// queueUpgrade enqueues a self-upgrade job for nodeID targeting panel Version.
func (s *Server) queueUpgrade(nodeID string) (*upgradeJob, error) {
	ver := strings.TrimSpace(s.Version)
	if ver == "" || ver == "dev" {
		return nil, fmt.Errorf("panel version is %q — cannot push upgrade (need release build)", ver)
	}
	if !strings.HasPrefix(ver, "v") {
		ver = "v" + ver
	}
	_, urls := agentReleaseURLs(ver)
	if len(urls) == 0 {
		return nil, fmt.Errorf("no release urls")
	}
	job := &upgradeJob{
		ID:      strings.ReplaceAll(uuid.NewString(), "-", ""),
		Version: ver,
		URLs:    urls,
		Asset:   fmt.Sprintf("mieru-panel-%s-linux-*.tar.gz", ver),
	}
	s.upgradeMu.Lock()
	s.upgradeJobs[nodeID] = job
	s.upgradeMu.Unlock()
	_ = s.store.HeartbeatEx(nodeID, "", "", "", map[string]string{
		"upgrade_status": "pending",
		"upgrade_target": ver,
		"upgrade_error":  "",
	})
	return job, nil
}

// peekUpgradeJob returns the pending job without removing it (re-delivered each
// heartbeat until agent reports result or version matches — works if agent restarts mid-upgrade).
func (s *Server) peekUpgradeJob(nodeID string) *upgradeJob {
	s.upgradeMu.Lock()
	defer s.upgradeMu.Unlock()
	return s.upgradeJobs[nodeID]
}

func (s *Server) clearUpgradeJob(nodeID string) {
	s.upgradeMu.Lock()
	delete(s.upgradeJobs, nodeID)
	s.upgradeMu.Unlock()
}

func (s *Server) upgradeNode(c *gin.Context) {
	id := c.Param("id")
	n, err := s.store.GetNode(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	job, err := s.queueUpgrade(n.ID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "node.upgrade", n.ID, "target="+job.Version)
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"node_id": n.ID,
		"job_id":  job.ID,
		"version": job.Version,
		"message": "已排队升级，节点下次心跳（≤5s）会开始下载并重启 agent",
	})
}

func (s *Server) upgradeAllNodes(c *gin.Context) {
	list, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var queued []string
	var skipped []string
	for _, n := range list {
		// Only online/degraded nodes can receive heartbeat jobs.
		if n.Status != model.StatusOnline && n.Status != model.StatusDegraded {
			skipped = append(skipped, n.ID+"(offline)")
			continue
		}
		if _, err := s.queueUpgrade(n.ID); err != nil {
			skipped = append(skipped, n.ID+"("+err.Error()+")")
			continue
		}
		queued = append(queued, n.ID)
	}
	s.store.Audit("admin", "node.upgrade_all", "", fmt.Sprintf("queued=%d", len(queued)))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"queued":  queued,
		"skipped": skipped,
		"version": s.Version,
		"message": fmt.Sprintf("已向 %d 个在线节点推送升级", len(queued)),
	})
}

func (s *Server) queuePanelURLJob(nodeID, url string) (*panelURLJob, error) {
	url = normalizePanelURL(url)
	if url == "" {
		return nil, fmt.Errorf("panel_url empty")
	}
	if strings.TrimSpace(nodeID) == "" {
		return nil, fmt.Errorf("node id empty")
	}
	job := &panelURLJob{
		ID:  strings.ReplaceAll(uuid.NewString(), "-", ""),
		URL: url,
	}
	s.panelURLMu.Lock()
	s.panelURLJobs[nodeID] = job
	s.panelURLMu.Unlock()
	_ = s.store.HeartbeatEx(nodeID, "", "", "", map[string]string{
		"panel_url_status": "pending",
		"panel_url_target": url,
		"panel_url_error":  "",
	})
	return job, nil
}

func (s *Server) peekPanelURLJob(nodeID string) *panelURLJob {
	s.panelURLMu.Lock()
	defer s.panelURLMu.Unlock()
	return s.panelURLJobs[nodeID]
}

func (s *Server) clearPanelURLJob(nodeID string) {
	s.panelURLMu.Lock()
	delete(s.panelURLJobs, nodeID)
	s.panelURLMu.Unlock()
}

func (s *Server) currentPanelURLSetting() string {
	u, _ := s.store.GetSetting("panel_url")
	return normalizePanelURL(u)
}

func (s *Server) syncNodePanelURL(c *gin.Context) {
	id := c.Param("id")
	n, err := s.store.GetNode(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "node not found"})
		return
	}
	url := s.currentPanelURLSetting()
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先在设置里保存「面板公网地址」"})
		return
	}
	// Optional override body: {"panel_url":"..."}
	var body struct {
		PanelURL string `json:"panel_url"`
	}
	_ = c.ShouldBindJSON(&body)
	if u := normalizePanelURL(body.PanelURL); u != "" {
		url = u
	}
	job, err := s.queuePanelURLJob(n.ID, url)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "node.sync_panel_url", n.ID, "url="+job.URL)
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"node_id": n.ID,
		"job_id":  job.ID,
		"url":     job.URL,
		"message": "已排队同步 PANEL_URL，节点下次心跳（≤5s）会改写 env 并重启 agent",
	})
}

func (s *Server) syncAllPanelURL(c *gin.Context) {
	url := s.currentPanelURLSetting()
	var body struct {
		PanelURL string `json:"panel_url"`
	}
	_ = c.ShouldBindJSON(&body)
	if u := normalizePanelURL(body.PanelURL); u != "" {
		url = u
	}
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请先在设置里保存「面板公网地址」"})
		return
	}
	list, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var queued []string
	var skipped []string
	for _, n := range list {
		if n.Status != model.StatusOnline && n.Status != model.StatusDegraded {
			skipped = append(skipped, n.ID+"(offline)")
			continue
		}
		if _, err := s.queuePanelURLJob(n.ID, url); err != nil {
			skipped = append(skipped, n.ID+"("+err.Error()+")")
			continue
		}
		queued = append(queued, n.ID)
	}
	s.store.Audit("admin", "node.sync_panel_url_all", "", fmt.Sprintf("url=%s queued=%d", url, len(queued)))
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"url":     url,
		"queued":  queued,
		"skipped": skipped,
		"message": fmt.Sprintf("已向 %d 个在线节点推送 PANEL_URL=%s", len(queued), url),
	})
}

func (s *Server) rebuildAll(c *gin.Context) {
	if err := s.rebuildNow("manual"); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "rebuild": s.rebuildStatusSnapshot()})
		return
	}
	s.store.Audit("admin", "rebuild_all", "*", "")
	// surface backbone (username only) so ops can confirm tunnel identity
	bbUser, _ := s.store.GetSetting(configgen.SettingBackboneUser)
	c.JSON(http.StatusOK, gin.H{"ok": true, "backbone_user": bbUser, "rebuild": s.rebuildStatusSnapshot()})
}

func (s *Server) getRebuildStatus(c *gin.Context) {
	c.JSON(http.StatusOK, s.rebuildStatusSnapshot())
}

// diagnose returns a human-readable health snapshot of the multi-hop data plane.
// Secrets (passwords) are never included — only usernames / hosts / plugin types.
func (s *Server) diagnose(c *gin.Context) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	users, _ := s.store.ListActiveProxyUsers()
	allUsers, _ := s.store.ListUsers()
	routes, _ := s.store.ListRoutes()
	bbUser, _ := s.store.GetSetting(configgen.SettingBackboneUser)
	bbPass, _ := s.store.GetSetting(configgen.SettingBackbonePass)
	panelVer := strings.TrimPrefix(strings.TrimSpace(s.Version), "v")
	now := time.Now()

	type issueItem struct {
		Text   string `json:"text"`
		Href   string `json:"href,omitempty"` // e.g. /nodes?tab=exit
		Kind   string `json:"kind,omitempty"` // node|route|user|global
		NodeID string `json:"node_id,omitempty"`
	}
	type nodeDiag struct {
		ID               string                   `json:"id"`
		Name             string                   `json:"name"`
		Role             string                   `json:"role"`
		Status           string                   `json:"status"`
		PublicIP         string                   `json:"public_ip"`
		PrivateIP        string                   `json:"private_ip"`
		DialHost         string                   `json:"dial_host"`
		PublicPort       int                      `json:"public_port"`
		MitaPort         int                      `json:"mita_port,omitempty"`
		ConfigVersion    int64                    `json:"config_version"`
		AgentConfigVer   int64                    `json:"agent_config_version,omitempty"`
		ConfigStale      bool                     `json:"config_stale,omitempty"`
		AgentVersion     string                   `json:"agent_version,omitempty"`
		VersionBehind    bool                     `json:"version_behind,omitempty"`
		HeartbeatAgeSec  int64                    `json:"heartbeat_age_sec,omitempty"`
		NoHeartbeat      bool                     `json:"no_heartbeat,omitempty"`
		TrafficReportAge int64                    `json:"traffic_report_age_sec,omitempty"`
		TrafficReporting bool                     `json:"traffic_reporting,omitempty"`
		MeteringCapable  bool                     `json:"metering_capable,omitempty"`
		MeteringHint     string                   `json:"metering_hint,omitempty"`
		Plugins          []map[string]interface{} `json:"plugins"`
		UserCount        int                      `json:"user_count"`
		Issues           []string                 `json:"issues"`
		IssueItems       []issueItem              `json:"issue_items,omitempty"`
	}
	out := make([]nodeDiag, 0, len(nodes))
	globalIssues := []string{}
	globalItems := []issueItem{}
	addGlobal := func(text, href string) {
		globalIssues = append(globalIssues, text)
		globalItems = append(globalItems, issueItem{Text: text, Href: href, Kind: "global"})
	}
	if len(users) == 0 {
		addGlobal("没有活跃代理用户 — mita/socks 可能拒绝启动", "/users")
	}
	if bbUser == "" || bbPass == "" {
		addGlobal("骨干链路凭证缺失 — 请点「重建配置」生成", "/")
	}
	enabledRoutes := 0
	for _, r := range routes {
		if r.Enabled {
			enabledRoutes++
		}
	}
	if enabledRoutes == 0 {
		addGlobal("没有启用中的隧道 — 请先建隧道", "/routes")
	}

	agentBehind := 0
	configStaleN := 0
	trafficSilent := 0

	for i := range nodes {
		applyOfflineStatus(&nodes[i], now)
		n := nodes[i]
		d := nodeDiag{
			ID:            n.ID,
			Name:          n.Name,
			Role:          n.Role,
			Status:        n.Status,
			PublicIP:      n.PublicIP,
			PrivateIP:     n.PrivateIP,
			DialHost:      n.DialHost(),
			PublicPort:    n.PublicServicePort(),
			ConfigVersion: n.ConfigVersion,
			Issues:        []string{},
			IssueItems:    []issueItem{},
		}
		if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
			d.MitaPort = n.MitaPrimaryPort()
		}
		if n.MetaJSON != "" {
			var meta map[string]interface{}
			if json.Unmarshal([]byte(n.MetaJSON), &meta) == nil {
				if v, ok := meta["agent_version"].(string); ok {
					d.AgentVersion = strings.TrimPrefix(strings.TrimSpace(v), "v")
				}
				switch cv := meta["agent_config_version"].(type) {
				case float64:
					d.AgentConfigVer = int64(cv)
				case string:
					if x, err := strconv.ParseInt(cv, 10, 64); err == nil {
						d.AgentConfigVer = x
					}
				}
			}
		}
		if d.AgentVersion != "" && panelVer != "" && d.AgentVersion != panelVer {
			d.VersionBehind = true
			agentBehind++
		}
		if d.AgentConfigVer > 0 && n.ConfigVersion > d.AgentConfigVer {
			d.ConfigStale = true
			configStaleN++
		}
		if n.LastSeen != nil {
			age := int64(now.Sub(*n.LastSeen).Seconds())
			if age < 0 {
				age = 0
			}
			d.HeartbeatAgeSec = age
			if age > int64(OfflineAfter.Seconds()) {
				d.NoHeartbeat = true
			}
		} else {
			d.NoHeartbeat = true
			d.HeartbeatAgeSec = -1
		}
		if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
			s.trafficMu.Lock()
			if t, ok := s.lastTraffic[n.ID]; ok && !t.IsZero() {
				age := int64(now.Sub(t).Seconds())
				if age < 0 {
					age = 0
				}
				d.TrafficReportAge = age
				d.TrafficReporting = age <= 30
			} else {
				d.TrafficReportAge = -1
			}
			s.trafficMu.Unlock()
			d.MeteringCapable = agentVersionAtLeast(d.AgentVersion, 0, 4, 17)
			if !d.TrafficReporting {
				trafficSilent++
			}
			switch {
			case d.AgentVersion == "":
				d.MeteringHint = "无 Agent 版本"
			case !d.MeteringCapable:
				d.MeteringHint = "需 ≥ v0.4.17"
			case !d.TrafficReporting:
				d.MeteringHint = "近期无流量上报"
			default:
				d.MeteringHint = "计量正常"
			}
		}

		addIssue := func(text string) {
			d.Issues = append(d.Issues, text)
			href := "/nodes"
			if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
				href = "/nodes?tab=exit"
			} else if n.Role == model.RoleRelay || n.Role == model.RoleEntry {
				href = "/nodes?tab=front"
			}
			d.IssueItems = append(d.IssueItems, issueItem{Text: text, Href: href, Kind: "node", NodeID: n.ID})
		}
		if n.DialHost() == "" {
			addIssue("无公网/内网/域名 — 上一跳无法拨入")
		}
		if n.Status != model.StatusOnline && n.Status != model.StatusDegraded {
			addIssue("Agent 离线或从未心跳")
		}
		if d.NoHeartbeat {
			addIssue(fmt.Sprintf("超过 %d 秒无心跳", int(OfflineAfter.Seconds())))
		}
		if n.Status == model.StatusDegraded {
			addIssue("Agent 降级（上次 apply 失败，查 journalctl -u mieru-agent）")
		}
		if d.VersionBehind {
			addIssue(fmt.Sprintf("Agent 版本落后（v%s → 面板 v%s）", d.AgentVersion, panelVer))
		}
		if d.ConfigStale {
			addIssue(fmt.Sprintf("配置未生效（面板 v%d / Agent 已应用 v%d）", n.ConfigVersion, d.AgentConfigVer))
		}
		if (n.Role == model.RoleExit || n.Role == model.RoleHybrid) && d.MeteringHint != "" && d.MeteringHint != "计量正常" {
			addIssue("流量计量：" + d.MeteringHint)
		}
		_, raw, err := s.store.GetDesiredConfig(n.ID)
		if err != nil || raw == "" {
			addIssue("无 desired 配置 — 需要重建")
		} else {
			var cfg model.AgentDesiredConfig
			if json.Unmarshal([]byte(raw), &cfg) == nil {
				d.UserCount = len(cfg.Users)
				for _, p := range cfg.Plugins {
					typ, _ := p["type"].(string)
					pc, _ := p["config"].(map[string]interface{})
					summary := map[string]interface{}{"type": typ}
					if pc != nil {
						for _, k := range []string{"listen_port", "port", "server", "upstream_host", "upstream_port", "via", "socks5_port", "port_min", "port_max", "exit_id"} {
							if v, ok := pc[k]; ok {
								summary[k] = v
							}
						}
						if _, ok := pc["link_user"]; ok {
							summary["link_user"] = pc["link_user"]
						}
					}
					d.Plugins = append(d.Plugins, summary)
				}
				has := map[string]bool{}
				for _, p := range cfg.Plugins {
					t, _ := p["type"].(string)
					has[t] = true
				}
				switch n.Role {
				case model.RoleExit:
					if !has["mita_server"] {
						addIssue("缺少 mita_server（落地未配置）")
					}
					if d.UserCount == 0 {
						addIssue("mita 用户数为 0")
					}
				case model.RoleRelay, model.RoleEntry:
					if has["tcp_forward"] {
						// ok
					} else if has["socks_in"] {
						addIssue("仍是 socks_in 链式（建议绑线路后重建为 tcp_forward）")
					} else {
						addIssue("缺少 tcp_forward（前置未指向落地，请建线路并重建配置）")
					}
				case model.RoleHybrid:
					if !has["mita_server"] {
						addIssue("hybrid 缺少 mita_server")
					}
					if !has["mieru_client"] && !has["tcp_forward"] {
						addIssue("hybrid 缺少 mieru_client")
					}
					if !has["socks_in"] && !has["tcp_forward"] {
						addIssue("hybrid 缺少对外监听（socks_in）")
					}
				}
			}
		}
		out = append(out, d)
	}

	// tunnel topo: one edge per enabled route (front → exit)
	type topoEdge struct {
		RouteID   int64  `json:"route_id"`
		Name      string `json:"name"`
		Health    string `json:"health"`
		FrontID   string `json:"front_id,omitempty"`
		FrontName string `json:"front_name,omitempty"`
		FrontHost string `json:"front_host,omitempty"`
		FrontPort int    `json:"front_port,omitempty"`
		ExitID    string `json:"exit_id,omitempty"`
		ExitName  string `json:"exit_name,omitempty"`
		ExitHost  string `json:"exit_host,omitempty"`
		ExitPort  int    `json:"exit_port,omitempty"`
		UserCount int    `json:"user_count"`
	}
	edges := make([]topoEdge, 0)
	userByRoute := map[int64]int{}
	for _, u := range allUsers {
		if u.RouteID != nil && *u.RouteID > 0 {
			userByRoute[*u.RouteID]++
		}
	}
	for i := range routes {
		r := &routes[i]
		if !r.Enabled {
			continue
		}
		v := s.enrichRoute(r)
		e := topoEdge{
			RouteID:   r.ID,
			Name:      r.Name,
			Health:    r.Health,
			FrontName: v.FrontName,
			FrontHost: v.FrontHost,
			FrontPort: v.FrontPort,
			ExitName:  v.ExitName,
			ExitHost:  v.ExitHost,
			ExitPort:  v.ExitPort,
			UserCount: userByRoute[r.ID],
		}
		var hops []model.Hop
		_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
		for _, h := range hops {
			if h.NodeID == "" {
				continue
			}
			nn, err := s.store.GetNode(h.NodeID)
			if err != nil {
				continue
			}
			if nn.Role == model.RoleExit || nn.Role == model.RoleHybrid {
				if e.ExitID == "" {
					e.ExitID = nn.ID
				}
			} else if nn.Role == model.RoleRelay || nn.Role == model.RoleEntry {
				if e.FrontID == "" {
					e.FrontID = nn.ID
				}
			}
		}
		edges = append(edges, e)
	}

	if agentBehind > 0 {
		addGlobal(fmt.Sprintf("%d 个节点 Agent 版本落后面板", agentBehind), "/nodes")
	}
	if configStaleN > 0 {
		addGlobal(fmt.Sprintf("%d 个节点配置未生效（desired > applied）", configStaleN), "/nodes")
	}
	if trafficSilent > 0 {
		addGlobal(fmt.Sprintf("%d 个落地近期无流量上报", trafficSilent), "/nodes?tab=exit")
	}

	// user ops cards: expiring soon / over quota / expired
	expiringSoon, overQuota, expiredN := 0, 0, 0
	for _, u := range allUsers {
		switch u.Status {
		case model.StatusOverQuota:
			overQuota++
		case model.StatusExpired:
			expiredN++
		}
		if u.ExpireAt != nil {
			days := u.ExpireAt.Sub(now).Hours() / 24
			if days >= 0 && days <= 3 {
				expiringSoon++
			}
		}
	}
	if expiringSoon > 0 {
		addGlobal(fmt.Sprintf("%d 个用户 3 天内到期", expiringSoon), "/users")
	}
	if overQuota > 0 {
		addGlobal(fmt.Sprintf("%d 个用户已超流量", overQuota), "/users")
	}

	// panel_url hint
	if s.store.PanelBaseURL() == "" {
		addGlobal("未设置对外面板地址 — 查询页/订阅链接可能变成 IP:端口", "/settings")
	}

	onlineN, offlineN := 0, 0
	for _, d := range out {
		if d.Status == model.StatusOnline {
			onlineN++
		} else if d.Status == model.StatusOffline || d.NoHeartbeat {
			offlineN++
		}
	}

	dash, _ := s.store.Dashboard()
	todayTotal := dash.TodayUp + dash.TodayDown
	monthTotal := dash.MonthUp + dash.MonthDown
	hourly, _ := s.store.TodayHourlyTraffic()
	if hourly == nil {
		hourly = []model.HourlyTrafficPoint{}
	}
	c.JSON(http.StatusOK, gin.H{
		"version":            s.Version,
		"backbone_user":      bbUser,
		"backbone_set":       bbUser != "" && bbPass != "",
		"active_users":       len(users),
		"enabled_routes":     enabledRoutes,
		"global_issues":      globalIssues,
		"global_issue_items": globalItems,
		"nodes":              out,
		"tunnel_edges":       edges,
		"rebuild":            s.rebuildStatusSnapshot(),
		"stats": gin.H{
			"agent_behind":   agentBehind,
			"config_stale":   configStaleN,
			"traffic_silent": trafficSilent,
			"panel_version":  panelVer,
			"expiring_soon":  expiringSoon,
			"over_quota":     overQuota,
			"expired_users":  expiredN,
			"online_nodes":   onlineN,
			"offline_nodes":  offlineN,
			"today_up":       dash.TodayUp,
			"today_down":     dash.TodayDown,
			"today_total":    todayTotal,
			"month_up":       dash.MonthUp,
			"month_down":     dash.MonthDown,
			"month_total":    monthTotal,
			"total_users":    dash.TotalUsers,
			"active_users":   dash.ActiveUsers,
		},
		"traffic_hourly": hourly,
		"topology_hint":  "手机 ──mierus──► 前置(tcp_forward) ──TCP──► 落地 mita ──► 家宽出口",
	})
}

func (s *Server) nodeDesiredConfig(c *gin.Context) {
	id := c.Param("id")
	ver, raw, err := s.store.GetDesiredConfig(id)
	if err != nil || raw == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no desired config — rebuild first"})
		return
	}
	// Redact passwords before returning to admin UI.
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		c.JSON(http.StatusOK, gin.H{"version": ver, "raw": raw})
		return
	}
	if users, ok := cfg["users"].([]interface{}); ok {
		for _, u := range users {
			if m, ok := u.(map[string]interface{}); ok {
				if _, has := m["password"]; has {
					m["password"] = "***"
				}
			}
		}
	}
	if plugins, ok := cfg["plugins"].([]interface{}); ok {
		for _, p := range plugins {
			if pm, ok := p.(map[string]interface{}); ok {
				if pc, ok := pm["config"].(map[string]interface{}); ok {
					if _, has := pc["link_password"]; has {
						pc["link_password"] = "***"
					}
					if users, ok := pc["users"].([]interface{}); ok {
						for _, u := range users {
							if m, ok := u.(map[string]interface{}); ok {
								if _, has := m["password"]; has {
									m["password"] = "***"
								}
							}
						}
					}
				}
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"version": ver, "config": cfg})
}

// routeView enriches a route with allocated front/exit ports for the tunnel list UI.
type routeView struct {
	model.Route
	FrontPort       int    `json:"front_port,omitempty"` // 前置入口端口（手机扫码连这个）
	ExitPort        int    `json:"exit_port,omitempty"`  // 落地 mita 端口
	FrontHost       string `json:"front_host,omitempty"`
	ExitHost        string `json:"exit_host,omitempty"`
	FrontName       string `json:"front_name,omitempty"`
	ExitName        string `json:"exit_name,omitempty"`
	UserCount       int    `json:"user_count,omitempty"`
	EntryEndpoint   string `json:"entry_endpoint,omitempty"` // host:port for copy
	PathSummary     string `json:"path_summary,omitempty"`   // 前置 IP:port → 落地 mita
	LastProbeAt     string `json:"last_probe_at,omitempty"`
	LastProbeHealth string `json:"last_probe_health,omitempty"`
}

func (s *Server) listRoutes(c *gin.Context) {
	list, err := s.store.ListRoutes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// user counts per route
	users, _ := s.store.ListUsers()
	byRoute := map[int64]int{}
	for _, u := range users {
		if u.RouteID != nil && *u.RouteID > 0 {
			byRoute[*u.RouteID]++
		}
	}
	out := make([]routeView, 0, len(list))
	for i := range list {
		v := s.enrichRoute(&list[i])
		v.UserCount = byRoute[list[i].ID]
		out = append(out, v)
	}
	c.JSON(http.StatusOK, out)
}

// enrichRoute fills front/exit port fields using the same allocator as configgen/share.
func (s *Server) enrichRoute(r *model.Route) routeView {
	v := routeView{Route: *r}
	if r == nil {
		return v
	}
	var hops []model.Hop
	_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
	var frontID string
	for _, h := range hops {
		if h.NodeID == "" || h.External {
			continue
		}
		n, err := s.store.GetNode(h.NodeID)
		if err != nil {
			continue
		}
		// exit/hybrid first so hybrid is not also counted as front
		if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
			if v.ExitName == "" {
				v.ExitName = n.Name
				v.ExitHost = n.DialHost()
				v.ExitPort = n.MitaPrimaryPort()
			}
			continue
		}
		if n.Role == model.RoleRelay || n.Role == model.RoleEntry {
			if frontID == "" {
				frontID = n.ID
				v.FrontName = n.Name
				// Client-facing: domain first when set (same as share/query page)
				v.FrontHost = n.ClientHost()
				if v.FrontHost == "" {
					if ip := strings.TrimSpace(n.PublicIP); ip != "" {
						v.FrontHost = ip
					} else if hn := strings.TrimSpace(n.Hostname); hn != "" {
						v.FrontHost = hn
					} else {
						v.FrontHost = n.DialHost()
					}
				}
			}
		}
	}
	// Per-route front listen port (multi-exit pool allocation, same as share/QR).
	if frontID != "" {
		if p := configgen.FrontListenPort(s.store, frontID, r); p > 0 {
			v.FrontPort = p
		} else if n, err := s.store.GetNode(frontID); err == nil {
			v.FrontPort = n.PublicServicePort()
		}
	}
	if v.FrontHost != "" && v.FrontPort > 0 {
		v.EntryEndpoint = fmt.Sprintf("%s:%d", v.FrontHost, v.FrontPort)
	}
	// 前置名 公网IP:入口 → 落地名 mita口
	frontPart := v.FrontName
	if frontPart == "" {
		frontPart = "前置"
	}
	if v.EntryEndpoint != "" {
		frontPart = fmt.Sprintf("%s %s", frontPart, v.EntryEndpoint)
	} else if v.FrontHost != "" {
		frontPart = fmt.Sprintf("%s %s", frontPart, v.FrontHost)
	}
	exitPart := v.ExitName
	if exitPart == "" {
		exitPart = "落地"
	}
	if v.ExitPort > 0 {
		exitPart = fmt.Sprintf("%s mita:%d", exitPart, v.ExitPort)
	}
	v.PathSummary = frontPart + " → " + exitPart
	s.probeMu.Lock()
	if snap, ok := s.lastProbe[r.ID]; ok && !snap.At.IsZero() {
		v.LastProbeAt = snap.At.UTC().Format(time.RFC3339)
		v.LastProbeHealth = snap.Health
	}
	s.probeMu.Unlock()
	return v
}

type frontPortClaim struct {
	routeID int64
	name    string
}

// frontPortClaims maps frontNodeID → port → owning tunnel for all enabled
// routes except excludeRouteID. Used for conflict checks and auto-allocation.
func (s *Server) frontPortClaims(excludeRouteID int64) map[string]map[int]frontPortClaim {
	others, _ := s.store.ListRoutes()
	used := map[string]map[int]frontPortClaim{}
	for i := range others {
		or := &others[i]
		if or.ID == excludeRouteID || !or.Enabled {
			continue
		}
		var oh []model.Hop
		_ = json.Unmarshal([]byte(or.HopsJSON), &oh)
		for _, h := range oh {
			if h.NodeID == "" || h.External {
				continue
			}
			n, err := s.store.GetNode(h.NodeID)
			if err != nil {
				continue
			}
			if n.Role != model.RoleRelay && n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
				continue
			}
			port := h.Port
			if port <= 0 {
				port = configgen.FrontListenPort(s.store, n.ID, or)
			}
			if port <= 0 {
				continue
			}
			if used[n.ID] == nil {
				used[n.ID] = map[int]frontPortClaim{}
			}
			if _, exists := used[n.ID][port]; !exists {
				used[n.ID][port] = frontPortClaim{routeID: or.ID, name: or.Name}
			}
		}
	}
	return used
}

// allocateFrontPort picks the first free port in the front's pool.
func (s *Server) allocateFrontPort(frontID string, excludeRouteID int64) (int, error) {
	n, err := s.store.GetNode(frontID)
	if err != nil {
		return 0, fmt.Errorf("前置节点不存在: %s", frontID)
	}
	if n.Role != model.RoleRelay && n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
		return 0, fmt.Errorf("节点 %s 不是前置", n.Name)
	}
	pmin, pmax := n.EffectivePortRange()
	used := s.frontPortClaims(excludeRouteID)[frontID]
	for p := pmin; p <= pmax; p++ {
		if used != nil {
			if _, ok := used[p]; ok {
				continue
			}
		}
		return p, nil
	}
	return 0, fmt.Errorf("前置「%s」端口池 %d–%d 已满（共 %d 个口均被其它隧道占用），请扩池或删隧道",
		n.Name, pmin, pmax, pmax-pmin+1)
}

// ensureUniqueFrontPorts enforces: same front → each tunnel a distinct listen port.
// Empty hop.Port is auto-filled with the next free port in the pool and written
// back into hopsJSON so the pin survives rebuilds (no two tunnels share a port).
func (s *Server) ensureUniqueFrontPorts(hopsJSON string, excludeRouteID int64) (string, error) {
	var hops []model.Hop
	if err := json.Unmarshal([]byte(hopsJSON), &hops); err != nil {
		return hopsJSON, fmt.Errorf("hops_json 无效")
	}
	used := s.frontPortClaims(excludeRouteID)
	// Also track ports we assign within this hops list (multi-front rare but safe).
	localUsed := map[string]map[int]bool{}

	changed := false
	for i := range hops {
		h := &hops[i]
		if h.NodeID == "" || h.External {
			continue
		}
		n, err := s.store.GetNode(h.NodeID)
		if err != nil {
			return hopsJSON, fmt.Errorf("节点不存在: %s", h.NodeID)
		}
		if n.Role != model.RoleRelay && n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
			continue
		}
		// Hybrid-as-front only when it is not also the exit of this chain alone —
		// still allocate a public port if hop is present.
		pmin, pmax := n.EffectivePortRange()
		port := h.Port
		if port <= 0 {
			// Auto-allocate first free in pool.
			p, err := s.allocateFrontPort(n.ID, excludeRouteID)
			if err != nil {
				return hopsJSON, err
			}
			// Skip ports already taken in this same hops list.
			for localUsed[n.ID] != nil && localUsed[n.ID][p] {
				p++
				if p > pmax {
					return hopsJSON, fmt.Errorf("前置「%s」端口池已满", n.Name)
				}
			}
			h.Port = p
			port = p
			changed = true
		}
		if port < pmin || port > pmax {
			return hopsJSON, fmt.Errorf("入口端口 %d 不在前置「%s」的端口池 %d–%d 内", port, n.Name, pmin, pmax)
		}
		if m := used[n.ID]; m != nil {
			if c, ok := m[port]; ok {
				return hopsJSON, fmt.Errorf("入口端口 %d 已被隧道「%s」(#%d) 占用 — 同一前置每条落地隧道必须不同入口端口", port, c.name, c.routeID)
			}
		}
		if localUsed[n.ID] == nil {
			localUsed[n.ID] = map[int]bool{}
		}
		if localUsed[n.ID][port] {
			return hopsJSON, fmt.Errorf("入口端口 %d 在本隧道 hops 中重复", port)
		}
		localUsed[n.ID][port] = true
	}
	if !changed {
		return hopsJSON, nil
	}
	raw, err := json.Marshal(hops)
	if err != nil {
		return hopsJSON, err
	}
	return string(raw), nil
}

// validateRouteFrontPorts is kept for callers that only need a check (no mutate).
func (s *Server) validateRouteFrontPorts(hopsJSON string, excludeRouteID int64) error {
	_, err := s.ensureUniqueFrontPorts(hopsJSON, excludeRouteID)
	return err
}

func (s *Server) createRoute(c *gin.Context) {
	var req model.Route
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	req.Enabled = true
	if req.HopsJSON == "" {
		req.HopsJSON = "[]"
	}
	// Same front + multiple exits: force distinct front listen ports (auto-pin if empty).
	pinned, err := s.ensureUniqueFrontPorts(req.HopsJSON, 0)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.HopsJSON = pinned
	if err := s.store.CreateRoute(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "create_route", fmt.Sprintf("%d", req.ID), req.Name)
	c.JSON(http.StatusCreated, s.enrichRoute(&req))
}

func (s *Server) updateRoute(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, err := s.store.GetRoute(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req model.Route
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	pinned, err := s.ensureUniqueFrontPorts(req.HopsJSON, id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.Name = req.Name
	r.Enabled = req.Enabled
	r.Strategy = req.Strategy
	r.HopsJSON = pinned
	r.Weight = req.Weight
	if req.Health != "" {
		r.Health = req.Health
	}
	if err := s.store.UpdateRoute(r); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, s.enrichRoute(r))
}

func (s *Server) deleteRoute(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := s.store.DeleteRoute(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// probeRoute tests hop-to-hop reachability (entry→relay, relay→exit), not panel→each hop.
// For each consecutive pair, dial is executed on the source node agent when online;
// external entry has no agent so that leg is informational only.
func (s *Server) probeRoute(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	r, err := s.store.GetRoute(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var hops []model.Hop
	_ = json.Unmarshal([]byte(r.HopsJSON), &hops)

	type hopResult struct {
		Label   string `json:"label"`
		Kind    string `json:"kind"` // leg|node|external|info
		From    string `json:"from,omitempty"`
		To      string `json:"to,omitempty"`
		Host    string `json:"host"`
		Port    int    `json:"port"`
		OK      bool   `json:"ok"`
		Latency int64  `json:"latency_ms"`
		Error   string `json:"error,omitempty"`
		NodeID  string `json:"node_id,omitempty"`
		FromID  string `json:"from_id,omitempty"`
		ToID    string `json:"to_id,omitempty"`
		Status  string `json:"agent_status,omitempty"`
		Via     string `json:"via,omitempty"` // agent|panel|skip
	}
	results := make([]hopResult, 0)
	allOK := true
	anyOK := false
	legCount := 0  // legs that were actually dial-tested
	skipCount := 0 // external / no-agent informational skips

	// Resolve hop endpoint for dial target (prefer private IP).
	type resolvedHop struct {
		hop    model.Hop
		node   *model.Node
		label  string
		host   string
		port   int
		kind   string // external|node
		online bool
	}
	resolved := make([]resolvedHop, 0, len(hops))
	for _, h := range hops {
		rh := resolvedHop{hop: h}
		if h.External || (h.NodeID == "" && h.Host != "") {
			rh.kind = "external"
			rh.host = strings.TrimSpace(h.Host)
			rh.port = h.Port
			if rh.port <= 0 {
				rh.port = 1080
			}
			rh.label = h.Name
			if rh.label == "" {
				rh.label = rh.host
			}
			resolved = append(resolved, rh)
			continue
		}
		if h.NodeID == "" {
			continue
		}
		n, err := s.store.GetNode(h.NodeID)
		if err != nil {
			rh.kind = "node"
			rh.label = h.NodeID
			resolved = append(resolved, rh)
			continue
		}
		rh.kind = "node"
		rh.node = n
		rh.label = n.Name
		rh.online = n.Status == model.StatusOnline || n.Status == model.StatusDegraded
		// Target port: next hop's service port depending on role.
		// exit/hybrid-as-exit → mita; otherwise public socks.
		switch n.Role {
		case model.RoleExit:
			rh.port = n.MitaPrimaryPort()
		case model.RoleHybrid:
			// When used as exit after a relay, dial mita; as entry/middle hop use socks.
			// Default to PublicServicePort; adjusted per leg below.
			rh.port = n.PublicServicePort()
		default:
			rh.port = n.PublicServicePort()
		}
		if h.Port > 0 {
			rh.port = h.Port
		}
		rh.host = n.DialHost()
		resolved = append(resolved, rh)
	}

	// Build consecutive legs: hop[i] → hop[i+1]
	for i := 0; i+1 < len(resolved); i++ {
		from := resolved[i]
		to := resolved[i+1]

		// Adjust target port: if destination is exit or hybrid after relay, use mita.
		toPort := to.port
		if to.node != nil {
			if to.node.Role == model.RoleExit {
				toPort = to.node.MitaPrimaryPort()
			} else if to.node.Role == model.RoleHybrid {
				// Prefer mita when source is a front/relay (tcp_forward or mieru → mita).
				if from.node != nil && (from.node.Role == model.RoleRelay || from.node.Role == model.RoleEntry || from.node.Role == model.RoleHybrid) {
					if from.node.Role == model.RoleRelay || from.node.Role == model.RoleEntry {
						toPort = to.node.MitaPrimaryPort()
					} else {
						toPort = to.node.PublicServicePort()
					}
				} else {
					toPort = to.node.PublicServicePort()
				}
			} else {
				toPort = to.node.PublicServicePort()
			}
			if to.hop.Port > 0 {
				toPort = to.hop.Port
			}
		}
		toHost := to.host
		if to.node != nil {
			toHost = to.node.DialHost()
		}

		hr := hopResult{
			Label:  from.label + " → " + to.label,
			Kind:   "leg",
			From:   from.label,
			To:     to.label,
			Host:   toHost,
			Port:   toPort,
			FromID: from.hop.NodeID,
			ToID:   to.hop.NodeID,
		}
		if to.node != nil {
			hr.NodeID = to.node.ID
			hr.Status = to.node.Status
		}

		// Merchant public IP / external entry has no agent — cannot dial from there.
		// This is informational only and must NOT mark the route unhealthy.
		// Real path check is front agent → exit mita (next legs).
		if from.kind == "external" || from.node == nil {
			hr.Via = "skip"
			hr.Kind = "external"
			hr.OK = true // not a failure — unprobeable by design
			hr.Error = "商家前置公网无 Agent，面板无法从 " + from.label + " 侧探测（属正常）。手机连 " +
				from.host + ":" + strconv.Itoa(from.port) + " 由商家 DNAT 到中继；关键链路看下一跳「前置→落地」。"
			if from.host != "" {
				hr.Host = from.host
				if from.port > 0 {
					hr.Port = from.port
				}
			}
			skipCount++
			results = append(results, hr)
			continue
		}

		if toHost == "" || toPort <= 0 {
			legCount++
			hr.Via = "skip"
			hr.OK = false
			hr.Error = "下一跳缺少地址/端口（请填写公网 IP 或内网 IP）"
			allOK = false
			results = append(results, hr)
			continue
		}

		legCount++
		// Prefer agent-side dial from source node.
		if from.online && from.node != nil {
			hr.Via = "agent"
			// Wait > 1 heartbeat cycle (agent hb ≈5s) + dial timeout.
			ok, lat, errMsg := s.requestAgentDial(from.node.ID, toHost, toPort, 30*time.Second)
			hr.OK = ok
			hr.Latency = lat
			if !ok {
				hr.Error = errMsg
				if hr.Error == "" {
					hr.Error = "agent dial failed"
				}
				allOK = false
			} else {
				anyOK = true
			}
			results = append(results, hr)
			continue
		}

		// Fallback: panel dials target (only useful when panel can reach private net — rare).
		hr.Via = "panel"
		addr := net.JoinHostPort(toHost, strconv.Itoa(toPort))
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 4*time.Second)
		hr.Latency = time.Since(start).Milliseconds()
		if err != nil {
			hr.OK = false
			hr.Error = "源节点 Agent 离线，面板代测失败: " + err.Error()
			allOK = false
		} else {
			_ = conn.Close()
			hr.OK = true
			anyOK = true
		}
		results = append(results, hr)
	}

	// Single-hop routes: no leg to test — report node self-listen from agent if possible.
	if legCount == 0 && skipCount == 0 && len(resolved) == 1 {
		rh := resolved[0]
		hr := hopResult{
			Label:  rh.label + "（单跳自检）",
			Kind:   "node",
			Host:   rh.host,
			Port:   rh.port,
			NodeID: rh.hop.NodeID,
		}
		if rh.node != nil {
			hr.Status = rh.node.Status
			hr.Host = rh.node.DialHost()
			hr.Port = rh.node.PublicServicePort()
		}
		if rh.node != nil && rh.online {
			hr.Via = "agent"
			ok, lat, errMsg := s.requestAgentDial(rh.node.ID, "127.0.0.1", hr.Port, 3*time.Second)
			hr.OK = ok
			hr.Latency = lat
			legCount++
			if !ok {
				hr.Error = errMsg
				allOK = false
			} else {
				anyOK = true
			}
		} else if rh.kind == "external" {
			hr.Via = "skip"
			hr.Kind = "external"
			hr.OK = true
			hr.Error = "外部入口无 Agent，无法自检"
			skipCount++
		} else {
			hr.Via = "skip"
			hr.OK = false
			hr.Error = "无法测通：无连续跳且节点离线"
			legCount++
			allOK = false
		}
		results = append(results, hr)
	}

	health := "unknown"
	if legCount == 0 {
		// Only external skips (or empty) — cannot prove path from agents.
		if skipCount > 0 {
			health = "unknown"
		} else {
			health = "unknown"
		}
	} else if allOK {
		health = "ok"
	} else if anyOK {
		health = "degraded"
	} else {
		health = "down"
	}
	_ = s.store.SetRouteHealth(id, health)
	r.Health = health
	checkedAt := time.Now().UTC()
	s.probeMu.Lock()
	s.lastProbe[id] = probeSnap{At: checkedAt, Health: health}
	s.probeMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"route_id":    id,
		"health":      health,
		"hops":        results,
		"checked_at":  checkedAt.Format(time.RFC3339),
		"tested_legs": legCount,
		"skipped":     skipCount,
		"note":        "关键：前置 Agent → 落地 mita。商家公网入口无 Agent，显示「不可测」属正常，不代表手机连不上。",
	})
}

func (s *Server) listUsers(c *gin.Context) {
	_ = s.store.RefreshUserStatuses()
	list, err := s.store.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	base := s.publicBase(c)
	routes, _ := s.store.ListRoutes()
	routeName := map[int64]string{}
	for _, r := range routes {
		routeName[r.ID] = r.Name
	}
	now := time.Now().Unix()
	type row struct {
		model.User
		UpBps        int64  `json:"up_bps"`
		DownBps      int64  `json:"down_bps"`
		RateTS       int64  `json:"rate_ts,omitempty"`
		Subscription string `json:"subscription"`
		InfoURL      string `json:"info_url,omitempty"` // shareable read-only page
		RouteName    string `json:"route_name,omitempty"`
		EntryDisplay string `json:"entry_display,omitempty"`
	}
	out := make([]row, 0, len(list))
	for _, u := range list {
		r := row{
			User:         u,
			Subscription: base + "/sub/" + u.SubToken,
			InfoURL:      base + "/u/" + u.SubToken,
		}
		if u.RouteID != nil {
			r.RouteName = routeName[*u.RouteID]
		}
		// client-facing entry for operator list
		if eps := s.resolveUserMitaEndpoints(&u); len(eps) > 0 {
			r.EntryDisplay = fmt.Sprintf("%s:%d", eps[0].Host, eps[0].Port)
		} else if strings.TrimSpace(u.EntryHost) != "" {
			if u.EntryPort > 0 {
				r.EntryDisplay = fmt.Sprintf("%s:%d", u.EntryHost, u.EntryPort)
			} else {
				r.EntryDisplay = u.EntryHost
			}
		}
		if sample, ok := s.store.GetRate(u.ID); ok {
			r.RateTS = sample.TS
			// stale (>15s) → show 0 so UI never freezes on last speed
			if sample.TS > 0 && now-sample.TS > 15 {
				r.UpBps = 0
				r.DownBps = 0
			} else {
				r.UpBps = sample.UpBps
				r.DownBps = sample.DownBps
			}
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) createUser(c *gin.Context) {
	var req struct {
		Username          string `json:"username"`
		ExpireAt          string `json:"expire_at"`
		TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
		SpeedLimitBps     int64  `json:"speed_limit_bps"`
		MaxSessions       int    `json:"max_sessions"`
		StickyExitID      string `json:"sticky_exit_id"`
		RouteID           *int64 `json:"route_id"`
		EntryHost         string `json:"entry_host"`
		EntryPort         int    `json:"entry_port"`
		Note              string `json:"note"`
		ProxyPassword     string `json:"proxy_password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请填写用户名"})
		return
	}
	// 用户名全局唯一 —— 重复时给中文提示，避免 SQLite UNIQUE 原文
	if existing, err := s.store.GetUserByUsername(req.Username); err == nil && existing != nil {
		c.JSON(http.StatusConflict, gin.H{
			"error": fmt.Sprintf("用户名「%s」已存在（#%d），请换一个名字，或直接使用列表里的该用户", req.Username, existing.ID),
		})
		return
	}
	u := &model.User{
		Username:          req.Username,
		TrafficLimitBytes: req.TrafficLimitBytes,
		SpeedLimitBps:     req.SpeedLimitBps,
		MaxSessions:       req.MaxSessions,
		StickyExitID:      req.StickyExitID,
		RouteID:           req.RouteID,
		EntryHost:         strings.TrimSpace(req.EntryHost),
		EntryPort:         req.EntryPort,
		Note:              req.Note,
		ProxyPassword:     req.ProxyPassword,
		Status:            model.StatusActive,
	}
	if req.ExpireAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpireAt); err == nil {
			u.ExpireAt = &t
		} else if t, err := time.Parse("2006-01-02", req.ExpireAt); err == nil {
			u.ExpireAt = &t
		}
	}
	if err := s.store.CreateUser(u); err != nil {
		msg := err.Error()
		if strings.Contains(strings.ToLower(msg), "unique") || strings.Contains(msg, "UNIQUE") {
			c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("用户名「%s」已存在，请换一个名字", req.Username)})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": msg})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "create_user", fmt.Sprintf("%d", u.ID), u.Username)
	base := s.publicBase(c)
	share := s.userSharePayload(u)
	c.JSON(http.StatusCreated, gin.H{
		"user":            u,
		"proxy_password":  u.ProxyPassword,
		"sub_token":       u.SubToken,
		"subscription":    base + "/sub/" + u.SubToken,
		"info_url":        base + "/u/" + u.SubToken,
		"share_url":       share["share_url"],
		"share_urls":      share["share_urls"],
		"entries":         share["entries"],
		"mihomo_yaml":     share["mihomo_yaml"],
		"mihomo_url":      base + "/sub/" + u.SubToken + "/mihomo.yaml",
		"clash_verge_url": base + "/sub/" + u.SubToken + "/mihomo.yaml",
	})
}

func (s *Server) getUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	sample, _ := s.store.GetRate(u.ID)
	share := s.userSharePayload(u)
	base := s.publicBase(c)
	c.JSON(http.StatusOK, gin.H{
		"user":            u,
		"rate":            sample,
		"subscription":    base + "/sub/" + u.SubToken,
		"share_url":       share["share_url"],
		"share_urls":      share["share_urls"],
		"entries":         share["entries"],
		"mihomo_yaml":     share["mihomo_yaml"],
		"mihomo_url":      base + "/sub/" + u.SubToken + "/mihomo.yaml",
		"clash_verge_url": base + "/sub/" + u.SubToken + "/mihomo.yaml",
	})
}

func (s *Server) updateUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Status            string  `json:"status"`
		ExpireAt          string  `json:"expire_at"`
		TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
		SpeedLimitBps     *int64  `json:"speed_limit_bps"`
		MaxSessions       *int    `json:"max_sessions"`
		StickyExitID      string  `json:"sticky_exit_id"`
		RouteID           *int64  `json:"route_id"`
		EntryHost         *string `json:"entry_host"`
		EntryPort         *int    `json:"entry_port"`
		Note              string  `json:"note"`
		ClearExpire       bool    `json:"clear_expire"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Status != "" {
		u.Status = req.Status
	}
	if req.ClearExpire {
		u.ExpireAt = nil
	} else if req.ExpireAt != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpireAt); err == nil {
			u.ExpireAt = &t
		} else if t, err := time.Parse("2006-01-02", req.ExpireAt); err == nil {
			u.ExpireAt = &t
		}
	}
	if req.TrafficLimitBytes != nil {
		u.TrafficLimitBytes = *req.TrafficLimitBytes
	}
	if req.SpeedLimitBps != nil {
		u.SpeedLimitBps = *req.SpeedLimitBps
	}
	if req.MaxSessions != nil {
		u.MaxSessions = *req.MaxSessions
	}
	u.StickyExitID = req.StickyExitID
	if req.RouteID != nil {
		if *req.RouteID <= 0 {
			u.RouteID = nil // unbind
		} else {
			u.RouteID = req.RouteID
		}
	}
	if req.EntryHost != nil {
		u.EntryHost = strings.TrimSpace(*req.EntryHost)
	}
	if req.EntryPort != nil {
		u.EntryPort = *req.EntryPort
	}
	u.Note = req.Note
	if err := s.store.UpdateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, u)
}

func (s *Server) deleteUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, _ := s.store.GetUser(id)
	name := ""
	if u != nil {
		name = u.Username
	}
	if err := s.store.DeleteUser(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "delete_user", fmt.Sprintf("%d", id), name)
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) resetUserPassword(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, _ := s.store.GetUser(id)
	pw, err := s.store.ResetUserProxyPassword(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	name := ""
	if u != nil {
		name = u.Username
	}
	s.store.Audit("admin", "reset_password", fmt.Sprintf("%d", id), name)
	c.JSON(http.StatusOK, gin.H{"proxy_password": pw, "username": name, "user_id": id})
}

func (s *Server) resetUserSub(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	tok, err := s.store.ResetSubToken(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	base := s.publicBase(c)
	s.store.Audit("admin", "reset_sub", fmt.Sprintf("%d", id), "")
	c.JSON(http.StatusOK, gin.H{
		"sub_token":       tok,
		"subscription":    base + "/sub/" + tok,
		"info_url":        base + "/u/" + tok,
		"mihomo_url":      base + "/sub/" + tok + "/mihomo.yaml",
		"clash_verge_url": base + "/sub/" + tok + "/mihomo.yaml",
	})
}

// renewUser extends expire_at by days, or sets absolute date; re-activates expired/over_quota.
func (s *Server) renewUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Days     int    `json:"days"`
		ExpireAt string `json:"expire_at"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	now := time.Now().UTC()
	if req.ExpireAt != "" {
		if t, err := time.Parse("2006-01-02", req.ExpireAt); err == nil {
			// end of that day UTC
			t = time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, time.UTC)
			u.ExpireAt = &t
		} else if t, err := time.Parse(time.RFC3339, req.ExpireAt); err == nil {
			u.ExpireAt = &t
		} else {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expire_at"})
			return
		}
	} else {
		days := req.Days
		if days <= 0 {
			days = 30
		}
		base := now
		if u.ExpireAt != nil && u.ExpireAt.After(now) {
			base = u.ExpireAt.UTC()
		}
		t := base.AddDate(0, 0, days)
		u.ExpireAt = &t
	}
	// re-activate expired; over_quota only if still under limit; disabled stays
	if u.Status == model.StatusExpired {
		u.Status = model.StatusActive
	} else if u.Status == model.StatusOverQuota {
		if u.TrafficLimitBytes == 0 || u.TrafficUsedBytes < u.TrafficLimitBytes {
			u.Status = model.StatusActive
		}
	}
	if err := s.store.UpdateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.store.RefreshUserStatuses()
	_ = s.gen.RebuildAll()
	u2, _ := s.store.GetUser(id)
	s.store.Audit("admin", "renew_user", fmt.Sprintf("%d", id), fmt.Sprintf("days=%d expire=%v", req.Days, req.ExpireAt))
	c.JSON(http.StatusOK, u2)
}

// addUserTraffic increases traffic_limit_bytes (or sets unlimited when unlimited=true).
func (s *Server) addUserTraffic(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		AddBytes  int64 `json:"add_bytes"`
		AddGB     int64 `json:"add_gb"`
		Unlimited bool  `json:"unlimited"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Unlimited {
		u.TrafficLimitBytes = 0
	} else {
		add := req.AddBytes
		if add <= 0 && req.AddGB > 0 {
			add = req.AddGB * 1024 * 1024 * 1024
		}
		if add <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "add_gb or add_bytes required"})
			return
		}
		// if previously unlimited (0), start from used so limit is used+add
		if u.TrafficLimitBytes == 0 {
			u.TrafficLimitBytes = u.TrafficUsedBytes + add
		} else {
			u.TrafficLimitBytes += add
		}
	}
	if u.Status == model.StatusOverQuota {
		if u.TrafficLimitBytes == 0 || u.TrafficUsedBytes < u.TrafficLimitBytes {
			u.Status = model.StatusActive
		}
	}
	if err := s.store.UpdateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.store.RefreshUserStatuses()
	_ = s.gen.RebuildAll()
	u2, _ := s.store.GetUser(id)
	s.store.Audit("admin", "add_traffic", fmt.Sprintf("%d", id), fmt.Sprintf("add_gb=%d unlimited=%v", req.AddGB, req.Unlimited))
	c.JSON(http.StatusOK, u2)
}

// toggleUser flips active <-> disabled.
func (s *Server) toggleUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Status string `json:"status"` // optional force: active|disabled
	}
	_ = c.BindJSON(&req)
	if req.Status == model.StatusActive || req.Status == model.StatusDisabled {
		u.Status = req.Status
	} else if u.Status == model.StatusDisabled {
		u.Status = model.StatusActive
	} else {
		u.Status = model.StatusDisabled
	}
	if err := s.store.UpdateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	u2, _ := s.store.GetUser(id)
	s.store.Audit("admin", "toggle_user", fmt.Sprintf("%d", id), u2.Status)
	c.JSON(http.StatusOK, u2)
}

// setUserDisplayMultiplier scales public query-page used/today/rate only.
// Real traffic_used_bytes and admin list stay unscaled.
func (s *Server) setUserDisplayMultiplier(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if _, err := s.store.GetUser(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Multiplier float64 `json:"multiplier"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	mult := req.Multiplier
	if mult <= 0 {
		mult = 1
	}
	if mult < 0.1 || mult > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multiplier must be between 0.1 and 100"})
		return
	}
	if err := s.store.SetUserDisplayMultiplier(id, mult); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	u2, _ := s.store.GetUser(id)
	s.store.Audit("admin", "display_multiplier", fmt.Sprintf("%d", id), fmt.Sprintf("%v", mult))
	c.JSON(http.StatusOK, u2)
}

// setUserSpeedLimit sets per-user max rate (bytes/sec, 0=unlimited). Triggers rebuild.
func (s *Server) setUserSpeedLimit(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	u, err := s.store.GetUser(id)
	if err != nil || u == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		// Mbps is operator-friendly; speed_limit_bps wins if both sent.
		Mbps          *float64 `json:"mbps"`
		SpeedLimitBps *int64   `json:"speed_limit_bps"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	var bps int64
	if req.SpeedLimitBps != nil {
		bps = *req.SpeedLimitBps
	} else if req.Mbps != nil {
		if *req.Mbps < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mbps must be >= 0"})
			return
		}
		// Mbps → bytes/sec (decimal megabit)
		bps = int64(*req.Mbps * 1000 * 1000 / 8)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "mbps or speed_limit_bps required"})
		return
	}
	if bps < 0 {
		bps = 0
	}
	// Cap absurd values (~10 Gbps)
	const maxBps = int64(10 * 1000 * 1000 * 1000 / 8)
	if bps > maxBps {
		c.JSON(http.StatusBadRequest, gin.H{"error": "speed limit too high (max 10000 Mbps)"})
		return
	}
	u.SpeedLimitBps = bps
	if err := s.store.UpdateUser(u); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	u2, _ := s.store.GetUser(id)
	mbps := float64(0)
	if bps > 0 {
		mbps = float64(bps) * 8 / 1e6
	}
	s.store.Audit("admin", "speed_limit", fmt.Sprintf("%d", id), fmt.Sprintf("%.3g Mbps (%d B/s)", mbps, bps))
	c.JSON(http.StatusOK, gin.H{
		"user":            u2,
		"speed_limit_bps": bps,
		"mbps":            mbps,
	})
}

func (s *Server) listRates(c *gin.Context) {
	// drop stale samples (>15s) so UI doesn't show frozen speeds
	rates := s.store.AllRates()
	now := time.Now().Unix()
	out := make([]model.TrafficSample, 0, len(rates))
	for _, r := range rates {
		if r.TS > 0 && now-r.TS > 15 {
			r.UpBps = 0
			r.DownBps = 0
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) listAudit(c *gin.Context) {
	limit := 200
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
			if limit > 500 {
				limit = 500
			}
		}
	}
	list, err := s.store.ListAudit(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	q := strings.ToLower(strings.TrimSpace(c.Query("q")))
	action := strings.ToLower(strings.TrimSpace(c.Query("action")))
	if q != "" || action != "" {
		filtered := make([]model.AuditLog, 0, len(list))
		for _, a := range list {
			if action != "" && !strings.Contains(strings.ToLower(a.Action), action) {
				continue
			}
			if q != "" {
				hay := strings.ToLower(a.Actor + " " + a.Action + " " + a.Target + " " + a.Detail)
				if !strings.Contains(hay, q) {
					continue
				}
			}
			filtered = append(filtered, a)
		}
		list = filtered
	}
	c.JSON(http.StatusOK, list)
}

// batchUsers bulk enable/disable/delete/renew/add-traffic for selected user IDs.
func (s *Server) batchUsers(c *gin.Context) {
	var req struct {
		IDs    []int64 `json:"ids"`
		Action string  `json:"action"` // enable|disable|delete|renew|add_traffic
		Days   int     `json:"days"`
		AddGB  float64 `json:"add_gb"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择用户"})
		return
	}
	if len(req.IDs) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "单次最多 200 个用户"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(req.Action))
	okN, failN := 0, 0
	var lastErr string
	needRebuild := false

	for _, id := range req.IDs {
		if id <= 0 {
			failN++
			continue
		}
		u, err := s.store.GetUser(id)
		if err != nil || u == nil {
			failN++
			lastErr = "user not found"
			continue
		}
		switch action {
		case "enable":
			if err := s.store.SetUserStatus(id, model.StatusActive); err != nil {
				failN++
				lastErr = err.Error()
				continue
			}
			okN++
			needRebuild = true
		case "disable":
			if err := s.store.SetUserStatus(id, model.StatusDisabled); err != nil {
				failN++
				lastErr = err.Error()
				continue
			}
			okN++
			needRebuild = true
		case "delete":
			if err := s.store.DeleteUser(id); err != nil {
				failN++
				lastErr = err.Error()
				continue
			}
			okN++
			needRebuild = true
		case "renew":
			days := req.Days
			if days <= 0 {
				days = 30
			}
			base := time.Now()
			if u.ExpireAt != nil && u.ExpireAt.After(base) {
				base = *u.ExpireAt
			}
			t := base.Add(time.Duration(days) * 24 * time.Hour)
			u.ExpireAt = &t
			if u.Status == model.StatusExpired {
				u.Status = model.StatusActive
			}
			if err := s.store.UpdateUser(u); err != nil {
				failN++
				lastErr = err.Error()
				continue
			}
			okN++
			needRebuild = true
		case "add_traffic":
			add := int64(req.AddGB * 1024 * 1024 * 1024)
			if add <= 0 {
				failN++
				lastErr = "add_gb required"
				continue
			}
			if u.TrafficLimitBytes <= 0 {
				// unlimited already — skip
				okN++
				continue
			}
			u.TrafficLimitBytes += add
			if u.Status == model.StatusOverQuota && u.TrafficUsedBytes < u.TrafficLimitBytes {
				u.Status = model.StatusActive
			}
			if err := s.store.UpdateUser(u); err != nil {
				failN++
				lastErr = err.Error()
				continue
			}
			okN++
			needRebuild = true
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown action: " + action})
			return
		}
	}

	if needRebuild {
		s.scheduleRebuild("batch_users:" + action)
	}
	s.store.Audit("admin", "batch_users", action, fmt.Sprintf("ok=%d fail=%d ids=%d", okN, failN, len(req.IDs)))
	out := gin.H{"ok": true, "action": action, "success": okN, "failed": failN}
	if lastErr != "" && failN > 0 {
		out["last_error"] = lastErr
	}
	c.JSON(http.StatusOK, out)
}

// exportBackup returns a JSON snapshot of settings/nodes/routes/users (no secrets hashed dumps).
func (s *Server) exportBackup(c *gin.Context) {
	nodes, err := s.store.ListNodes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	routes, _ := s.store.ListRoutes()
	users, _ := s.store.ListUsers()
	settings, _ := s.store.GetSettings(
		"panel_url", "panel_name", "panel_subtitle",
		configgen.SettingBackboneUser,
	)
	// strip sensitive fields from nodes (agent tokens)
	safeNodes := make([]gin.H, 0, len(nodes))
	for _, n := range nodes {
		safeNodes = append(safeNodes, gin.H{
			"id":             n.ID,
			"name":           n.Name,
			"role":           n.Role,
			"status":         n.Status,
			"public_ip":      n.PublicIP,
			"private_ip":     n.PrivateIP,
			"hostname":       n.Hostname,
			"listen_port":    n.ListenPort,
			"port_min":       n.PortMin,
			"port_max":       n.PortMax,
			"public_port":    n.PublicServicePort(),
			"mita_port":      n.MitaPrimaryPort(),
			"config_version": n.ConfigVersion,
			"last_seen":      n.LastSeen,
			// agent_token intentionally omitted from default export
		})
	}
	safeUsers := make([]gin.H, 0, len(users))
	for _, u := range users {
		row := gin.H{
			"id":                  u.ID,
			"username":            u.Username,
			"status":              u.Status,
			"expire_at":           u.ExpireAt,
			"traffic_limit_bytes": u.TrafficLimitBytes,
			"traffic_used_bytes":  u.TrafficUsedBytes,
			"route_id":            u.RouteID,
			"entry_host":          u.EntryHost,
			"entry_port":          u.EntryPort,
			"note":                u.Note,
			"display_multiplier":  u.DisplayMultiplier,
			"sub_token":           u.SubToken,
			"created_at":          u.CreatedAt,
		}
		safeUsers = append(safeUsers, row)
	}
	s.store.Audit("admin", "export_backup", "*", fmt.Sprintf("nodes=%d routes=%d users=%d", len(nodes), len(routes), len(users)))
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="mieru-panel-backup-%s.json"`, time.Now().UTC().Format("20060102-150405")))
	c.JSON(http.StatusOK, gin.H{
		"format":           "mieru-panel-backup",
		"format_version":   1,
		"secrets_included": false,
		"exported_at":      time.Now().UTC().Format(time.RFC3339),
		"version":          s.Version,
		"panel_version":    s.Version,
		"settings":         settings,
		"nodes":            safeNodes,
		"routes":           routes,
		"users":            safeUsers,
		"note":             "安全备份：不含 agent_token / 管理员密码哈希 / 用户代理密码。不能用于换机导入；换机请用 GET /api/admin/migration/export。",
	})
}

// exportMigration downloads a full secret-inclusive package for moving the panel host.
func (s *Server) exportMigration(c *gin.Context) {
	snap, err := s.store.ExportMigration(s.Version)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "export_migration", "*", fmt.Sprintf(
		"nodes=%d routes=%d users=%d anns=%d traffic=%d",
		len(snap.Nodes), len(snap.Routes), len(snap.Users), len(snap.Announcements), len(snap.TrafficHourly),
	))
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Disposition", fmt.Sprintf(
		`attachment; filename="mieru-panel-migration-full-%s.json"`,
		time.Now().UTC().Format("20060102-150405"),
	))
	c.Header("X-Mieru-Migration-Format", store.MigrationFormat)
	c.JSON(http.StatusOK, snap)
}

// importMigration replaces this panel's DB with a migration package (destructive).
// Requires header X-Confirm-Import: 1
func (s *Server) importMigration(c *gin.Context) {
	if c.GetHeader("X-Confirm-Import") != "1" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少确认头 X-Confirm-Import: 1"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<20)
	var snap store.MigrationSnapshot
	if err := c.ShouldBindJSON(&snap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "无效 JSON: " + err.Error()})
		return
	}
	if err := store.ValidateMigrationSnapshot(&snap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.store.ImportMigration(&snap); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rebuildOK := true
	rebuildErr := ""
	if err := s.gen.RebuildAll(); err != nil {
		rebuildOK = false
		rebuildErr = err.Error()
		log.Printf("migration import rebuild: %v", err)
	}
	s.store.Audit("admin", "import_migration", "*", fmt.Sprintf(
		"nodes=%d routes=%d users=%d anns=%d traffic=%d rebuild_ok=%v",
		len(snap.Nodes), len(snap.Routes), len(snap.Users), len(snap.Announcements), len(snap.TrafficHourly), rebuildOK,
	))
	c.JSON(http.StatusOK, gin.H{
		"ok":            true,
		"nodes":         len(snap.Nodes),
		"routes":        len(snap.Routes),
		"users":         len(snap.Users),
		"announcements": len(snap.Announcements),
		"traffic_rows":  len(snap.TrafficHourly),
		"settings":      len(snap.Settings),
		"admins":        len(snap.Admins),
		"rebuild_ok":    rebuildOK,
		"rebuild_error": rebuildErr,
		"exported_at":   snap.ExportedAt,
		"panel_version": snap.PanelVersion,
		"hint":          "导入完成。若面板 IP/域名已变：1) 设置里改「面板公网地址」2) 设置里点「同步 PANEL_URL 到节点」（节点需仍能连上当前面板一次）；离线节点仍需 SSH 改 /etc/mieru-agent.env。管理员请用迁移包内的旧密码登录。",
	})
}

func (s *Server) myProfile(c *gin.Context) {
	claims := c.MustGet("claims").(*auth.Claims)
	if claims.Role == "admin" {
		c.JSON(http.StatusOK, gin.H{"role": "admin", "username": claims.Username})
		return
	}
	u, err := s.store.GetUserByUsername(claims.Username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	u.PasswordHash = ""
	sample, _ := s.store.GetRate(u.ID)
	up, down := s.store.TodayTrafficByUser(u.ID)
	entries := []gin.H{}
	nodes, _ := s.store.ListNodes()
	for _, n := range nodes {
		if n.Role == model.RoleEntry || n.Role == model.RoleHybrid {
			host := n.PublicHost()
			if host == "" {
				continue
			}
			entries = append(entries, gin.H{"name": n.Name, "host": host, "status": n.Status, "region": n.Region})
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"user":         u,
		"rate":         sample,
		"today_up":     up,
		"today_down":   down,
		"subscription": s.publicBase(c) + "/sub/" + u.SubToken,
		"entries":      entries,
	})
}

// publicUserInfo is a no-auth read-only snapshot for end users.
// Uses the same sub_token as /sub/:token. Never returns proxy_password / password_hash.
func (s *Server) publicUserInfo(c *gin.Context) {
	tok := strings.TrimSpace(c.Param("token"))
	if tok == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing token"})
		return
	}
	u, err := s.store.GetUserBySubToken(tok)
	if err != nil || u == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "链接无效或已失效"})
		return
	}
	_ = s.store.RefreshUserStatuses()
	if u2, err := s.store.GetUser(u.ID); err == nil && u2 != nil {
		u = u2
	}
	// Keep proxy password only for building share/YAML (same capability as /sub/:token).
	// Never return password_hash or a standalone proxy_password field.
	share := s.userSharePayload(u)
	u.PasswordHash = ""
	u.ProxyPassword = ""

	sample, hasRate := s.store.GetRate(u.ID)
	todayUp, todayDown := s.store.TodayTrafficByUser(u.ID)

	// Display multiplier: scale used / today / rate for query page only.
	// Quota (limit) stays real so ring fills faster when mult > 1.
	mult := u.DisplayMultiplier
	if mult <= 0 {
		mult = 1
	}
	scaleBytes := func(n int64) int64 {
		if mult == 1 {
			return n
		}
		return int64(math.Round(float64(n) * mult))
	}
	dispUsed := scaleBytes(u.TrafficUsedBytes)
	dispTodayUp := scaleBytes(todayUp)
	dispTodayDown := scaleBytes(todayDown)
	var rateOut interface{}
	if !hasRate {
		rateOut = gin.H{"up_bps": 0, "down_bps": 0}
	} else {
		rateOut = gin.H{
			"up_bps":     scaleBytes(sample.UpBps),
			"down_bps":   scaleBytes(sample.DownBps),
			"up_bytes":   scaleBytes(sample.UpBytes),
			"down_bytes": scaleBytes(sample.DownBytes),
			"ts":         sample.TS,
			"user_id":    sample.UserID,
		}
	}

	routeName := ""
	entryDisplay := ""
	if u.RouteID != nil {
		if r, err := s.store.GetRoute(*u.RouteID); err == nil && r != nil {
			routeName = r.Name
		}
	}
	// Query page: show host only (no :port) — port stays in QR/YAML/share.
	if eps := s.resolveUserMitaEndpoints(u); len(eps) > 0 {
		entryDisplay = strings.TrimSpace(eps[0].Host)
	} else if strings.TrimSpace(u.EntryHost) != "" {
		entryDisplay = strings.TrimSpace(u.EntryHost)
	}

	base := s.publicBase(c)
	var expireStr interface{}
	if u.ExpireAt != nil {
		expireStr = u.ExpireAt.UTC().Format("2006-01-02")
	} else {
		expireStr = nil
	}

	// panel brand for page header
	brandName, _ := s.store.GetSetting("panel_name")
	if strings.TrimSpace(brandName) == "" {
		brandName = "Mieru"
	}
	brandNameEN, _ := s.store.GetSetting("panel_name_en")
	brandNameEN = strings.TrimSpace(brandNameEN)
	locale, _ := s.store.GetSetting("user_info_locale")
	locale = strings.ToLower(strings.TrimSpace(locale))
	if locale != "en" {
		locale = "zh"
	}
	displayName := brandName
	if locale == "en" && brandNameEN != "" {
		displayName = brandNameEN
	}

	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.JSON(http.StatusOK, gin.H{
		"panel_name":       displayName,
		"panel_name_zh":    brandName,
		"panel_name_en":    brandNameEN,
		"user_info_locale": locale,
		"username":   u.Username,
		"status":     u.Status,
		"expire_at":  expireStr,
		// scaled for display
		"traffic_used_bytes": dispUsed,
		// real quota (not scaled)
		"traffic_limit_bytes": u.TrafficLimitBytes,
		"today_up":            dispTodayUp,
		"today_down":          dispTodayDown,
		"rate":                rateOut,
		"display_multiplier":  mult,
		"route_name":          routeName,
		"entry":               entryDisplay,
		"note":                u.Note,
		// convenience links
		"info_url":        base + "/u/" + tok,
		"subscription":    base + "/sub/" + tok,
		"mihomo_url":      base + "/sub/" + tok + "/mihomo.yaml",
		"clash_verge_url": base + "/sub/" + tok + "/mihomo.yaml",
		// same payload as admin share modal (QR / YAML) — no standalone password field
		"share_url":   share["share_url"],
		"share_urls":  share["share_urls"],
		"entries":     share["entries"],
		"mihomo_yaml": share["mihomo_yaml"],
	})
}

func (s *Server) myRate(c *gin.Context) {
	claims := c.MustGet("claims").(*auth.Claims)
	if claims.Role == "admin" {
		c.JSON(http.StatusOK, s.store.AllRates())
		return
	}
	u, err := s.store.GetUserByUsername(claims.Username)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	sample, _ := s.store.GetRate(u.ID)
	c.JSON(http.StatusOK, sample)
}

// ---------- Settings & install helpers ----------

type installInfo struct {
	PanelURL string
	Cmd      string
	Hint     string
}

func (s *Server) publicBase(c *gin.Context) string {
	if u := s.store.PanelBaseURL(); u != "" {
		return u
	}
	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	if p := c.GetHeader("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := c.Request.Host
	if host == "" {
		host = "127.0.0.1:8080"
	}
	return scheme + "://" + host
}

func normalizePanelURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	raw = strings.TrimRight(raw, "/")
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "http://" + raw
	}
	return raw
}

// panelURLEqual compares panel base URLs ignoring trailing slash and default ports.
func panelURLEqual(a, b string) bool {
	a = normalizePanelURL(a)
	b = normalizePanelURL(b)
	if a == "" || b == "" {
		return a == b
	}
	if a == b {
		return true
	}
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if !strings.EqualFold(ua.Scheme, ub.Scheme) {
		return false
	}
	ha := strings.ToLower(ua.Hostname())
	hb := strings.ToLower(ub.Hostname())
	if ha != hb {
		return false
	}
	pa, pb := ua.Port(), ub.Port()
	if pa == "" {
		if strings.EqualFold(ua.Scheme, "https") {
			pa = "443"
		} else {
			pa = "80"
		}
	}
	if pb == "" {
		if strings.EqualFold(ub.Scheme, "https") {
			pb = "443"
		} else {
			pb = "80"
		}
	}
	if pa != pb {
		return false
	}
	paPath := strings.TrimRight(ua.Path, "/")
	pbPath := strings.TrimRight(ub.Path, "/")
	return paPath == pbPath
}

func (s *Server) buildInstallCmd(c *gin.Context, n *model.Node) installInfo {
	base := s.publicBase(c)
	role := n.Role
	if role == "" {
		role = "exit"
	}
	// Agent-only one-liner (no ufw/firewall-cmd noise).
	cmd := fmt.Sprintf(
		"curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash -s -- --panel-url %s --node-id %s --token %s --role %s",
		base, n.ID, n.AgentToken, role,
	)
	hint := "在对应 Linux 节点上整行粘贴执行即可安装/升级 Agent。请先在「设置」填写面板公网地址。"
	if s.store.PanelBaseURL() == "" {
		hint = "尚未配置面板地址，当前用浏览器访问地址生成命令。生产环境请到「设置」填写固定面板地址。"
	}
	return installInfo{PanelURL: base, Cmd: cmd, Hint: hint}
}

// publicBrand returns panel display name for unauthenticated pages (login, title, favicon).
func (s *Server) publicBrand(c *gin.Context) {
	c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
	c.Header("Pragma", "no-cache")
	m, _ := s.store.GetSettings("panel_name", "panel_name_en", "panel_subtitle", "panel_favicon", "user_info_locale")
	name := strings.TrimSpace(m["panel_name"])
	if name == "" {
		name = "Mieru"
	}
	nameEN := strings.TrimSpace(m["panel_name_en"])
	sub := strings.TrimSpace(m["panel_subtitle"])
	if sub == "" {
		sub = "管理节点、用户、隧道与落地计量"
	}
	locale := strings.ToLower(strings.TrimSpace(m["user_info_locale"]))
	if locale != "en" {
		locale = "zh"
	}
	c.JSON(http.StatusOK, gin.H{
		"panel_name":       name,
		"panel_name_en":    nameEN,
		"panel_subtitle":   sub,
		"favicon_data":     strings.TrimSpace(m["panel_favicon"]),
		"user_info_locale": locale,
		"version":          s.Version,
	})
}

func (s *Server) getSettings(c *gin.Context) {
	m, err := s.store.GetSettings(
		"panel_url", "panel_name", "panel_name_en", "panel_subtitle", "panel_favicon",
		"user_info_locale",
		configgen.SettingBackboneUser,
		"cf_api_token", "cf_zone_id", "cf_proxied_default",
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	panelURL := m["panel_url"]
	if panelURL == "" {
		panelURL = s.publicBase(c)
	}
	name := m["panel_name"]
	if name == "" {
		name = "Mieru"
	}
	nameEN := strings.TrimSpace(m["panel_name_en"])
	sub := strings.TrimSpace(m["panel_subtitle"])
	jwtDefault := s.cfg.JWTSecret == "change-me-in-production-please" || s.cfg.JWTSecret == "change-me-in-production"
	corsWide := len(s.cfg.CORSOrigins) == 1 && s.cfg.CORSOrigins[0] == "*"
	cfTok := strings.TrimSpace(m["cf_api_token"])
	cfZone := strings.TrimSpace(m["cf_zone_id"])
	locale := strings.ToLower(strings.TrimSpace(m["user_info_locale"]))
	if locale != "en" {
		locale = "zh"
	}
	c.JSON(http.StatusOK, gin.H{
		"panel_url":          panelURL,
		"panel_name":         name,
		"panel_name_en":      nameEN,
		"panel_subtitle":     sub,
		"panel_favicon":      strings.TrimSpace(m["panel_favicon"]),
		"user_info_locale":   locale,
		"panel_url_set":      m["panel_url"] != "",
		"version":            s.Version,
		"admin_user":         s.cfg.AdminUser,
		"backbone_user":      m[configgen.SettingBackboneUser],
		"backbone_ready":     m[configgen.SettingBackboneUser] != "",
		"jwt_is_default":     jwtDefault,
		"cors_wide_open":     corsWide,
		"cors_origins":       s.cfg.CORSOrigins,
		"cf_configured":      cfTok != "" && cfZone != "",
		"cf_zone_id":         cfZone,
		"cf_token_set":       cfTok != "",
		"cf_proxied_default": m["cf_proxied_default"] == "1" || strings.EqualFold(m["cf_proxied_default"], "true"),
		"security_hints": func() []string {
			hints := []string{}
			if jwtDefault {
				hints = append(hints, "PANEL_JWT_SECRET 仍为默认值，请在 /etc/mieru-panel.env 设置强随机密钥后重启面板")
			}
			if corsWide {
				hints = append(hints, "PANEL_CORS=*（宽松跨域）。同域部署可忽略；若仅本机访问可设为具体 Origin")
			}
			return hints
		}(),
	})
}

func (s *Server) putSettings(c *gin.Context) {
	var req struct {
		PanelURL      string `json:"panel_url"`
		PanelName     string `json:"panel_name"`
		PanelNameEN   string `json:"panel_name_en"`
		PanelSubtitle string `json:"panel_subtitle"`
		PanelFavicon  string `json:"panel_favicon"` // data URL or empty to clear
		// User query page language: "zh" | "en"
		UserInfoLocale *string `json:"user_info_locale"`
		// Cloudflare (optional). Empty cf_api_token keeps existing; "clear" removes.
		CFAPIToken       *string `json:"cf_api_token"`
		CFZoneID         *string `json:"cf_zone_id"`
		CFProxiedDefault *bool   `json:"cf_proxied_default"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	url := normalizePanelURL(req.PanelURL)
	if url == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "panel_url required (e.g. http://IP:8080 or https://panel.example.com)"})
		return
	}
	if err := s.store.SetSetting("panel_url", url); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.PanelName)
	// Collapse internal whitespace runs for display consistency.
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "Mieru"
	}
	// Avoid absurdly long sidebar titles (UTF-8 runes).
	if rn := []rune(name); len(rn) > 32 {
		name = string(rn[:32])
	}
	if err := s.store.SetSetting("panel_name", name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	nameEN := strings.Join(strings.Fields(strings.TrimSpace(req.PanelNameEN)), " ")
	if rn := []rune(nameEN); len(rn) > 48 {
		nameEN = string(rn[:48])
	}
	if err := s.store.SetSetting("panel_name_en", nameEN); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	sub := strings.Join(strings.Fields(strings.TrimSpace(req.PanelSubtitle)), " ")
	if rn := []rune(sub); len(rn) > 80 {
		sub = string(rn[:80])
	}
	if err := s.store.SetSetting("panel_subtitle", sub); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// favicon: allow empty (clear) or data:image/...; base64,... max ~200KB raw in settings
	fav := strings.TrimSpace(req.PanelFavicon)
	if fav != "" {
		if !strings.HasPrefix(fav, "data:image/") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "favicon 须为 data:image/... 格式"})
			return
		}
		if len(fav) > 280000 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "图标过大（请用 ≤100KB 的 PNG/SVG）"})
			return
		}
	}
	if err := s.store.SetSetting("panel_favicon", fav); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	localeOut := "zh"
	if req.UserInfoLocale != nil {
		loc := strings.ToLower(strings.TrimSpace(*req.UserInfoLocale))
		if loc == "en" {
			localeOut = "en"
		} else {
			localeOut = "zh"
		}
		if err := s.store.SetSetting("user_info_locale", localeOut); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	} else if v, _ := s.store.GetSetting("user_info_locale"); strings.ToLower(strings.TrimSpace(v)) == "en" {
		localeOut = "en"
	}
	if req.CFZoneID != nil {
		if err := s.store.SetSetting("cf_zone_id", strings.TrimSpace(*req.CFZoneID)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	if req.CFAPIToken != nil {
		tok := strings.TrimSpace(*req.CFAPIToken)
		if tok == "" || strings.EqualFold(tok, "clear") {
			_ = s.store.SetSetting("cf_api_token", "")
		} else if tok != "********" && !strings.HasPrefix(tok, "••••") {
			if err := s.store.SetSetting("cf_api_token", tok); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
		}
	}
	if req.CFProxiedDefault != nil {
		v := "0"
		if *req.CFProxiedDefault {
			v = "1"
		}
		_ = s.store.SetSetting("cf_proxied_default", v)
	}
	s.store.Audit("admin", "update_settings", "panel", url)
	cfTok, _ := s.store.GetSetting("cf_api_token")
	cfZone, _ := s.store.GetSetting("cf_zone_id")
	c.JSON(http.StatusOK, gin.H{
		"ok":               true,
		"panel_url":        url,
		"panel_name":       name,
		"panel_name_en":    nameEN,
		"panel_subtitle":   sub,
		"panel_favicon":    fav,
		"user_info_locale": localeOut,
		"cf_configured":    strings.TrimSpace(cfTok) != "" && strings.TrimSpace(cfZone) != "",
		"cf_zone_id":       strings.TrimSpace(cfZone),
		"cf_token_set":     strings.TrimSpace(cfTok) != "",
	})
}

// putUserInfoLocale toggles the public user query page language (zh|en) without other settings.
func (s *Server) putUserInfoLocale(c *gin.Context) {
	var req struct {
		Locale string `json:"user_info_locale"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	loc := strings.ToLower(strings.TrimSpace(req.Locale))
	if loc != "en" {
		loc = "zh"
	}
	if err := s.store.SetSetting("user_info_locale", loc); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "update_user_info_locale", "panel", loc)
	c.JSON(http.StatusOK, gin.H{"ok": true, "user_info_locale": loc})
}

func (s *Server) cfClient() (*cloudflare.Client, error) {
	tok, _ := s.store.GetSetting("cf_api_token")
	zone, _ := s.store.GetSetting("cf_zone_id")
	if strings.TrimSpace(tok) == "" || strings.TrimSpace(zone) == "" {
		return nil, fmt.Errorf("请先在设置页填写 Cloudflare API Token 与 Zone ID")
	}
	return cloudflare.New(tok, zone), nil
}

// cloudflareUpsertDNS creates/updates A/AAAA for a node domain → IP.
// Body: { name, ip, proxied? }. Proxied defaults to false (DNS only) for custom ports.
func (s *Server) cloudflareUpsertDNS(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		IP      string `json:"ip"`
		Proxied *bool  `json:"proxied"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	cli, err := s.cfClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	proxied := false
	if req.Proxied != nil {
		proxied = *req.Proxied
	} else {
		v, _ := s.store.GetSetting("cf_proxied_default")
		proxied = v == "1" || strings.EqualFold(v, "true")
	}
	// Custom non-HTTP ports cannot go through orange-cloud proxy.
	if proxied {
		// still allow if user insists, but warn in response
	}
	rec, err := cli.UpsertA(req.Name, req.IP, proxied)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "cf_dns_upsert", req.Name, fmt.Sprintf("%s → %s proxied=%v", rec.Type, req.IP, proxied))
	note := ""
	if proxied {
		note = "已开启 CF 代理（橙云）。自定义非 80/443 端口可能不可用，入口建议关闭代理（仅 DNS）。"
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"record":  rec,
		"name":    rec.Name,
		"type":    rec.Type,
		"content": rec.Content,
		"proxied": rec.Proxied,
		"note":    note,
	})
}

// cloudflareLookupDNS finds A/AAAA names in the configured zone that point to ?ip=
// so operators can fill 接入域名 from existing CF records.
func (s *Server) cloudflareLookupDNS(c *gin.Context) {
	ip := strings.TrimSpace(c.Query("ip"))
	if ip == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 ip 参数"})
		return
	}
	cli, err := s.cfClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	recs, err := cli.FindHostsByIP(ip)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	names := make([]string, 0, len(recs))
	seen := map[string]bool{}
	for _, r := range recs {
		n := strings.TrimSpace(r.Name)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"ip":      ip,
		"names":   names,
		"records": recs,
		"count":   len(names),
	})
}

func (s *Server) cloudflareTest(c *gin.Context) {
	cli, err := s.cfClient()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := cli.VerifyToken(); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token 无效: " + err.Error()})
		return
	}
	zn, err := cli.ZoneName()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Zone 无效: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "zone_name": zn})
}

func (s *Server) changeAdminPassword(c *gin.Context) {
	var req struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.NewPassword = strings.TrimSpace(req.NewPassword)
	if req.NewPassword == "" || len(req.NewPassword) < 6 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_password min 6 chars"})
		return
	}
	claims, err := s.bearer(c)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	user := claims.Username
	if req.Username != "" {
		user = req.Username
	}
	adm, err := s.store.GetAdminByUsername(claims.Username)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	if !store.CheckPassword(adm.PasswordHash, req.CurrentPassword) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password wrong"})
		return
	}
	if err := s.store.SetAdminPassword(user, req.NewPassword); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "change_password", user, "")
	c.JSON(http.StatusOK, gin.H{
		"ok":       true,
		"username": user,
		"hint":     "密码已更新到数据库。若 /etc/mieru-panel.env 里 PANEL_ADMIN_PASS 不同，可手动同步，勿设 PANEL_ADMIN_FORCE_SYNC=1 除非要强制用 env 覆盖。",
	})
}

func (s *Server) nodeInstallCmd(c *gin.Context) {
	n, err := s.store.GetNode(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	info := s.buildInstallCmd(c, n)
	c.JSON(http.StatusOK, gin.H{
		"node_id":     n.ID,
		"role":        n.Role,
		"agent_token": n.AgentToken,
		"panel_url":   info.PanelURL,
		"install_cmd": info.Cmd,
		"hint":        info.Hint,
	})
}

// shareEndpoint is a client-facing mieru (mita) endpoint for QR / import.
type shareEndpoint struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"` // TCP|UDP
}

// clientShareName is what clients show as node remark / list title after scan.
// Matches common panel style: username-M月D日 (e.g. kelly-8月6日). Permanent → username only.
func clientShareName(u *model.User) string {
	if u == nil {
		return ""
	}
	name := strings.TrimSpace(u.Username)
	if name == "" {
		return ""
	}
	if u.ExpireAt != nil {
		t := u.ExpireAt.UTC()
		// date-only expire is stored end-of-day; show calendar day in UTC (panel dates are date-only)
		return fmt.Sprintf("%s-%d月%d日", name, int(t.Month()), t.Day())
	}
	return name
}

// resolveUserMitaEndpoints returns endpoints for official mierus:// share links.
//
// Product path (国内前置 + 美国家宽落地 / TK):
//
//	phone ──mierus──► front(entry|relay) public port ──tcp_forward──► exit mita
//
// The share link advertises the **front** host:port (what the phone can reach).
// Auth and egress still happen on the **exit** mita (residential). Front is a
// transparent TCP pipe (not socks5, not a second mita).
//
// Priority:
//  1. user.EntryHost / EntryPort (manual advertise)
//  2. route: first agent hop that is entry/relay (front) + its public listen port
//  3. route: first/last exit/hybrid (single-node or client can dial exit directly)
//  4. all exit/hybrid nodes
func (s *Server) resolveUserMitaEndpoints(u *model.User) []shareEndpoint {
	seen := map[string]bool{}
	out := []shareEndpoint{}

	add := func(name, host string, port int) {
		host = strings.TrimSpace(host)
		if host == "" || port <= 0 {
			return
		}
		key := fmt.Sprintf("%s:%d", host, port)
		if seen[key] {
			return
		}
		seen[key] = true
		if name == "" {
			name = host
		}
		out = append(out, shareEndpoint{Name: name, Host: host, Port: port, Protocol: "TCP"})
	}

	var (
		frontHost string
		frontPort int
		frontName string
		frontID   string
		exitNode  *model.Node
		route     *model.Route
	)
	if u != nil && u.RouteID != nil {
		if r, err := s.store.GetRoute(*u.RouteID); err == nil && r.Enabled {
			route = r
			var hops []model.Hop
			_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
			for _, h := range hops {
				if h.NodeID == "" || h.External {
					continue
				}
				n, err := s.store.GetNode(h.NodeID)
				if err != nil {
					continue
				}
				// First entry/relay = domestic front (tcp_forward listen).
				if frontHost == "" && (n.Role == model.RoleEntry || n.Role == model.RoleRelay) {
					frontHost = n.ClientHost()
					frontID = n.ID
					frontName = n.Name
					if frontName == "" {
						frontName = frontHost
					}
					// Per-route front port (multi-exit: 10401→exitA, 10402→exitB).
					if p := configgen.FrontListenPort(s.store, n.ID, r); p > 0 {
						frontPort = p
					} else if h.Port > 0 {
						frontPort = h.Port
					} else {
						frontPort = n.PublicServicePort()
					}
				}
				if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
					if exitNode == nil {
						exitNode = n
					}
				}
			}
			// If front was found but port still 0, re-resolve via allocator.
			if frontID != "" && frontPort <= 0 && route != nil {
				if p := configgen.FrontListenPort(s.store, frontID, route); p > 0 {
					frontPort = p
				}
			}
		}
	}

	// Manual advertise (operator front IP, e.g. 移动入口 211.x).
	if u != nil {
		host := strings.TrimSpace(u.EntryHost)
		if host != "" {
			port := u.EntryPort
			if port <= 0 {
				if frontPort > 0 {
					port = frontPort
				} else if exitNode != nil {
					port = exitNode.MitaPrimaryPort()
				}
			}
			if port <= 0 {
				port = 8964
			}
			// temporary internal name; rewritten to clientShareName below
			add(host, host, port)
			return applyClientShareNames(u, out)
		}
	}

	// Multi-hop: advertise front (cm7), not the US public IP.
	if frontHost != "" && frontPort > 0 {
		add(frontName, frontHost, frontPort)
		return applyClientShareNames(u, out)
	}

	// Single-node / direct exit: client dials mita on the exit itself.
	if exitNode != nil {
		host := exitNode.ClientHost()
		port := exitNode.MitaPrimaryPort()
		name := exitNode.Name
		if name == "" {
			name = host
		}
		add(name, host, port)
		return applyClientShareNames(u, out)
	}

	// Fallback: all exit/hybrid.
	nodes, _ := s.store.ListNodes()
	for _, n := range nodes {
		if n.Role != model.RoleExit && n.Role != model.RoleHybrid {
			continue
		}
		host := n.ClientHost()
		port := n.MitaPrimaryPort()
		name := n.Name
		if name == "" {
			name = host
		}
		add(name, host, port)
	}
	return applyClientShareNames(u, out)
}

// applyClientShareNames sets endpoint display names for client remark / node list.
// Format: username-M月D日 (e.g. kelly-8月6日); permanent → username only.
func applyClientShareNames(u *model.User, out []shareEndpoint) []shareEndpoint {
	label := clientShareName(u)
	if label == "" {
		return out
	}
	for i := range out {
		if len(out) == 1 {
			out[i].Name = label
		} else {
			out[i].Name = fmt.Sprintf("%s-%d", label, i+1)
		}
	}
	return out
}

// mierusShareURL builds official simple share link (enfein/mieru simple export):
//
//	mierus://user:pass@host?handshake-mode=...&mtu=...&multiplexing=...&port=N&profile=NAME&protocol=TCP
//
// Official client uses query param **profile** as the profile/list display name
// (NOT the URL #fragment). Empty name falls back to "default".
func mierusShareURL(username, password, host string, port int, protocol, name string) string {
	if host == "" || port <= 0 {
		return ""
	}
	if protocol == "" {
		protocol = "TCP"
	}
	protocol = strings.ToUpper(protocol)
	// IPv6 host needs brackets in URL authority — url.URL handles via JoinHostPort-less Host.
	hostPart := host
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		hostPart = "[" + host + "]"
	}
	profile := strings.TrimSpace(name)
	if profile == "" {
		profile = "default"
	}
	// profile is a single token in official format; strip chars that break query parsing.
	profile = strings.ReplaceAll(profile, "&", "-")
	profile = strings.ReplaceAll(profile, "?", "-")
	profile = strings.ReplaceAll(profile, "#", "-")
	q := url.Values{}
	q.Set("handshake-mode", "HANDSHAKE_NO_WAIT")
	q.Set("mtu", "1400")
	q.Set("multiplexing", "MULTIPLEXING_OFF")
	q.Set("port", strconv.Itoa(port))
	q.Set("profile", profile)
	q.Set("protocol", protocol)
	u := url.URL{
		Scheme:   "mierus",
		User:     url.UserPassword(username, password),
		Host:     hostPart,
		RawQuery: q.Encode(),
	}
	// Fragment kept as secondary hint for clients that read #remark; official mieru uses profile.
	if profile != "" && profile != "default" {
		u.Fragment = profile
	}
	return u.String()
}

func (s *Server) userSharePayload(u *model.User) gin.H {
	endpoints := s.resolveUserMitaEndpoints(u)
	links := make([]gin.H, 0, len(endpoints))
	primary := ""
	for _, e := range endpoints {
		link := mierusShareURL(u.Username, u.ProxyPassword, e.Host, e.Port, e.Protocol, e.Name)
		if link == "" {
			continue
		}
		if primary == "" {
			primary = link
		}
		links = append(links, gin.H{
			"name":     e.Name,
			"host":     e.Host,
			"port":     e.Port,
			"protocol": e.Protocol,
			"url":      link,
		})
	}
	joined := ""
	if len(links) > 0 {
		parts := make([]string, 0, len(links))
		for _, l := range links {
			if s, ok := l["url"].(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		joined = strings.Join(parts, "\n")
	}
	yamlBody := buildMihomoYAML(u, endpoints)
	return gin.H{
		"username":       u.Username,
		"proxy_password": u.ProxyPassword,
		"entries":        links,
		"share_url":      primary, // mierus:// — encode in QR
		"share_urls":     joined,
		"sub_token":      u.SubToken,
		"protocol":       "mieru",
		"mihomo_yaml":    yamlBody,
	}
}

// yamlQuote escapes a string for double-quoted YAML scalar.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// buildMihomoYAML produces a ready-to-import Clash Meta / Mihomo config.
// Proxy type "mieru" matches MetaCubeX docs (server/port/username/password/transport/multiplexing).
// Default rule mode: China / private → DIRECT, everything else → PROXY.
func buildMihomoYAML(u *model.User, endpoints []shareEndpoint) string {
	var b strings.Builder
	b.WriteString("# Mihomo / Clash Meta — generated by mieru-panel\n")
	b.WriteString("# user: " + u.Username + "\n")
	b.WriteString("# path: phone → front (mieru) → residential mita egress\n")
	b.WriteString("# routing: CN + private = DIRECT; foreign = PROXY\n")
	b.WriteString("# Import: Profiles → New → Remote (Clash Verge URL) or paste YAML\n")
	b.WriteString("# Client mode must be 「规则 / Rule」 (not Global)\n")
	b.WriteString("#\n")
	b.WriteString("mixed-port: 7890\n")
	b.WriteString("allow-lan: false\n")
	b.WriteString("mode: rule\n")
	b.WriteString("log-level: info\n")
	b.WriteString("ipv6: false\n")
	b.WriteString("\n")
	// DNS: domestic first + foreign fallback so CN sites resolve to mainland IPs (DIRECT path).
	b.WriteString("dns:\n")
	b.WriteString("  enable: true\n")
	b.WriteString("  listen: 0.0.0.0:1053\n")
	b.WriteString("  ipv6: false\n")
	b.WriteString("  enhanced-mode: fake-ip\n")
	b.WriteString("  fake-ip-range: 198.18.0.1/16\n")
	b.WriteString("  fake-ip-filter:\n")
	b.WriteString("    - '*.lan'\n")
	b.WriteString("    - '*.local'\n")
	b.WriteString("    - localhost\n")
	b.WriteString("    - '+.msftconnecttest.com'\n")
	b.WriteString("    - '+.msftncsi.com'\n")
	b.WriteString("    - 'time.*.com'\n")
	b.WriteString("    - 'time.*.gov'\n")
	b.WriteString("    - 'time.*.apple.com'\n")
	b.WriteString("    - 'ntp.*.com'\n")
	b.WriteString("  nameserver:\n")
	b.WriteString("    - https://doh.pub/dns-query\n")
	b.WriteString("    - https://dns.alidns.com/dns-query\n")
	b.WriteString("    - 223.5.5.5\n")
	b.WriteString("  fallback:\n")
	b.WriteString("    - https://1.1.1.1/dns-query\n")
	b.WriteString("    - https://8.8.8.8/dns-query\n")
	b.WriteString("    - 1.1.1.1\n")
	b.WriteString("    - 8.8.8.8\n")
	b.WriteString("  fallback-filter:\n")
	b.WriteString("    geoip: true\n")
	b.WriteString("    geoip-code: CN\n")
	b.WriteString("    ipcidr:\n")
	b.WriteString("      - 240.0.0.0/4\n")
	b.WriteString("\n")
	b.WriteString("proxies:\n")

	names := make([]string, 0, len(endpoints))
	if len(endpoints) == 0 {
		b.WriteString("  # no endpoint — bind route + set front entry, then rebuild\n")
	}
	for i, e := range endpoints {
		name := e.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", u.Username, i+1)
		}
		// Clash proxy name: avoid raw quotes/newlines
		name = strings.ReplaceAll(name, "\n", " ")
		name = strings.ReplaceAll(name, `"`, "'")
		names = append(names, name)
		proto := strings.ToUpper(e.Protocol)
		if proto != "UDP" {
			proto = "TCP"
		}
		b.WriteString("  - name: " + yamlQuote(name) + "\n")
		b.WriteString("    type: mieru\n")
		b.WriteString("    server: " + yamlQuote(e.Host) + "\n")
		b.WriteString("    port: " + strconv.Itoa(e.Port) + "\n")
		b.WriteString("    username: " + yamlQuote(u.Username) + "\n")
		b.WriteString("    password: " + yamlQuote(u.ProxyPassword) + "\n")
		b.WriteString("    transport: " + proto + "\n")
		// Align with official share defaults (OneClick / mierus://)
		b.WriteString("    multiplexing: MULTIPLEXING_OFF\n")
		// Optional fields supported by recent Meta builds:
		b.WriteString("    # handshake-mode: HANDSHAKE_NO_WAIT\n")
		b.WriteString("    # udp: true\n")
		b.WriteString("\n")
	}

	b.WriteString("proxy-groups:\n")
	b.WriteString("  - name: " + yamlQuote("PROXY") + "\n")
	b.WriteString("    type: select\n")
	b.WriteString("    proxies:\n")
	if len(names) == 0 {
		b.WriteString("      - DIRECT\n")
	} else {
		for _, n := range names {
			b.WriteString("      - " + yamlQuote(n) + "\n")
		}
		b.WriteString("      - DIRECT\n")
	}
	b.WriteString("\n")
	// Split tunnel: LAN/private + China → DIRECT; rest → selected node.
	// GEOSITE/GEOIP use Mihomo built-in GeoIP / Geosite databases (Clash Verge ships them).
	b.WriteString("rules:\n")
	b.WriteString("  # local / private\n")
	b.WriteString("  - GEOIP,PRIVATE,DIRECT,no-resolve\n")
	b.WriteString("  - GEOSITE,private,DIRECT\n")
	b.WriteString("  # China domains + IPs stay on domestic network\n")
	b.WriteString("  - GEOSITE,cn,DIRECT\n")
	b.WriteString("  - GEOIP,CN,DIRECT\n")
	b.WriteString("  # everything else via node\n")
	b.WriteString("  - MATCH,PROXY\n")
	return b.String()
}

func (s *Server) getUserShare(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	payload := s.userSharePayload(u)
	base := s.publicBase(c)
	payload["mihomo_url"] = base + "/sub/" + u.SubToken + "/mihomo.yaml"
	payload["clash_verge_url"] = base + "/sub/" + u.SubToken + "/mihomo.yaml"
	payload["mihomo_download"] = base + "/api/admin/users/" + strconv.FormatInt(u.ID, 10) + "/mihomo.yaml"
	c.JSON(http.StatusOK, payload)
}

func (s *Server) getUserMihomoYAML(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	endpoints := s.resolveUserMitaEndpoints(u)
	body := buildMihomoYAML(u, endpoints)
	fname := "mihomo-" + u.Username + ".yaml"
	c.Header("Content-Disposition", "attachment; filename="+fname)
	c.Data(http.StatusOK, "application/x-yaml; charset=utf-8", []byte(body))
}

func (s *Server) subscription(c *gin.Context) {
	u, err := s.store.GetUserBySubToken(c.Param("token"))
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	_ = s.store.RefreshUserStatuses()
	if u2, err := s.store.GetUser(u.ID); err == nil {
		u = u2
	}
	if u.Status != model.StatusActive {
		c.String(http.StatusForbidden, "account not active: "+u.Status)
		return
	}

	// One mierus:// link per line (official client / OneClick). Not Clash socks5.
	endpoints := s.resolveUserMitaEndpoints(u)
	var b strings.Builder
	b.WriteString("# mieru-panel\n")
	b.WriteString("# user: " + u.Username + "\n")
	if len(endpoints) == 0 {
		b.WriteString("# no exit/hybrid mita endpoint — bind route and rebuild\n")
	} else {
		for _, e := range endpoints {
			link := mierusShareURL(u.Username, u.ProxyPassword, e.Host, e.Port, e.Protocol, e.Name)
			if link != "" {
				b.WriteString(link)
				b.WriteByte('\n')
			}
		}
	}
	c.Header("Content-Disposition", "attachment; filename=subscription.txt")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(b.String()))
}

func (s *Server) subscriptionMihomo(c *gin.Context) {
	u, err := s.store.GetUserBySubToken(c.Param("token"))
	if err != nil {
		c.String(http.StatusNotFound, "not found")
		return
	}
	_ = s.store.RefreshUserStatuses()
	if u2, err := s.store.GetUser(u.ID); err == nil {
		u = u2
	}
	if u.Status != model.StatusActive {
		c.String(http.StatusForbidden, "account not active: "+u.Status)
		return
	}
	endpoints := s.resolveUserMitaEndpoints(u)
	body := buildMihomoYAML(u, endpoints)
	fname := "mihomo-" + u.Username + ".yaml"
	// Clash Verge / Mihomo remote profile headers.
	// Prefer inline over attachment — some clients fail import on forced download.
	c.Header("Content-Disposition", "inline; filename="+fname)
	c.Header("Profile-Update-Interval", "24")
	c.Header("Profile-Title", "base64:"+base64.StdEncoding.EncodeToString([]byte(u.Username)))
	expire := int64(0)
	if u.ExpireAt != nil && !u.ExpireAt.IsZero() {
		expire = u.ExpireAt.Unix()
	}
	c.Header("Subscription-Userinfo", fmt.Sprintf(
		"upload=%d; download=%d; total=%d; expire=%d",
		0, u.TrafficUsedBytes, u.TrafficLimitBytes, expire,
	))
	c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(body))
}

func (s *Server) agentHeartbeat(c *gin.Context) {
	var req model.HeartbeatRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if _, err := s.store.GetNodeByToken(req.NodeID, req.Token); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	status := model.StatusOnline
	if req.Message == "degraded" || strings.TrimSpace(req.ApplyError) != "" {
		status = model.StatusDegraded
	}
	metaPatch := map[string]string{
		"agent_version":        strings.TrimSpace(req.AgentVersion),
		"agent_config_version": strconv.FormatInt(req.ConfigVersion, 10),
	}
	if status == model.StatusDegraded {
		if ae := strings.TrimSpace(req.ApplyError); ae != "" {
			metaPatch["apply_error"] = ae
		} else {
			metaPatch["apply_error"] = "degraded (no detail — upgrade agent)"
		}
	} else {
		metaPatch["apply_error"] = "" // clear previous error
	}
	agentPanelURL := normalizePanelURL(req.PanelURL)
	if agentPanelURL != "" {
		metaPatch["agent_panel_url"] = agentPanelURL
	}
	_ = s.store.HeartbeatEx(req.NodeID, req.PublicIP, req.Hostname, status, metaPatch)
	n, _ := s.store.GetNode(req.NodeID)
	needPull := n != nil && n.ConfigVersion > req.ConfigVersion
	// Drain pending dial jobs for this agent (hop-to-hop probe).
	jobs := s.takeDialJobs(req.NodeID)
	// Self-upgrade: keep delivering until agent reports or already on target.
	var upJob *upgradeJob
	if job := s.peekUpgradeJob(req.NodeID); job != nil {
		agentVer := strings.TrimPrefix(strings.TrimSpace(req.AgentVersion), "v")
		wantVer := strings.TrimPrefix(strings.TrimSpace(job.Version), "v")
		if agentVer != "" && wantVer != "" && agentVer == wantVer {
			s.clearUpgradeJob(req.NodeID)
			_ = s.store.HeartbeatEx(req.NodeID, "", "", "", map[string]string{
				"upgrade_status": "ok",
				"upgrade_target": job.Version,
				"upgrade_error":  "",
				"agent_version":  agentVer,
			})
		} else {
			upJob = job
		}
	}
	// Auto-heal: agent reached us but still has a different PANEL_URL (post-migration IP/domain drift).
	wantPanel := s.currentPanelURLSetting()
	if wantPanel != "" && agentPanelURL != "" && !panelURLEqual(agentPanelURL, wantPanel) {
		if s.peekPanelURLJob(req.NodeID) == nil {
			if _, err := s.queuePanelURLJob(req.NodeID, wantPanel); err != nil {
				log.Printf("auto panel-url queue node=%s: %v", req.NodeID, err)
			} else {
				log.Printf("auto panel-url heal node=%s agent=%s want=%s", req.NodeID, agentPanelURL, wantPanel)
			}
		}
	} else if wantPanel != "" && agentPanelURL != "" && panelURLEqual(agentPanelURL, wantPanel) {
		// URL already correct: drop job + sticky pending/error meta.
		if job := s.peekPanelURLJob(req.NodeID); job != nil {
			s.clearPanelURLJob(req.NodeID)
		}
		_ = s.store.HeartbeatEx(req.NodeID, "", "", "", map[string]string{
			"panel_url_status": "ok",
			"panel_url_error":  "",
			"panel_url_target": wantPanel,
		})
	}
	// Panel URL rewrite: keep delivering until agent reports success.
	var urlJob *panelURLJob
	if job := s.peekPanelURLJob(req.NodeID); job != nil {
		urlJob = job
	}
	cfgVer := int64(0)
	if n != nil {
		cfgVer = n.ConfigVersion
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":             true,
		"config_version": cfgVer,
		"need_pull":      needPull,
		"dial_jobs":      jobs,
		"upgrade_job":    upJob,
		"panel_url_job":  urlJob,
	})
}

func (s *Server) agentUpgradeResult(c *gin.Context) {
	var req struct {
		NodeID  string `json:"node_id"`
		Token   string `json:"token"`
		JobID   string `json:"job_id"`
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		Error   string `json:"error"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if _, err := s.store.GetNodeByToken(req.NodeID, req.Token); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	patch := map[string]string{}
	if req.OK {
		s.clearUpgradeJob(req.NodeID)
		patch["upgrade_status"] = "ok"
		patch["upgrade_error"] = ""
		if v := strings.TrimSpace(req.Version); v != "" {
			patch["agent_version"] = strings.TrimPrefix(v, "v")
			patch["upgrade_target"] = strings.TrimPrefix(v, "v")
		}
	} else {
		// Keep job queued so a capable agent can retry; surface error in UI.
		patch["upgrade_status"] = "error"
		errMsg := strings.TrimSpace(req.Error)
		if errMsg == "" {
			errMsg = "upgrade failed"
		}
		if len(errMsg) > 500 {
			errMsg = errMsg[:500] + "…"
		}
		patch["upgrade_error"] = errMsg
	}
	_ = s.store.HeartbeatEx(req.NodeID, "", "", "", patch)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) agentPanelURLResult(c *gin.Context) {
	var req struct {
		NodeID string `json:"node_id"`
		Token  string `json:"token"`
		JobID  string `json:"job_id"`
		OK     bool   `json:"ok"`
		URL    string `json:"url"`
		Error  string `json:"error"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if _, err := s.store.GetNodeByToken(req.NodeID, req.Token); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	patch := map[string]string{}
	if req.OK {
		s.clearPanelURLJob(req.NodeID)
		patch["panel_url_status"] = "ok"
		patch["panel_url_error"] = ""
		if u := normalizePanelURL(req.URL); u != "" {
			patch["panel_url_target"] = u
		}
	} else {
		// Keep job queued so agent can retry; surface error in UI.
		patch["panel_url_status"] = "error"
		errMsg := strings.TrimSpace(req.Error)
		if errMsg == "" {
			errMsg = "panel url update failed"
		}
		if len(errMsg) > 500 {
			errMsg = errMsg[:500] + "…"
		}
		patch["panel_url_error"] = errMsg
	}
	_ = s.store.HeartbeatEx(req.NodeID, "", "", "", patch)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
func (s *Server) agentDialResult(c *gin.Context) {
	var req struct {
		NodeID  string `json:"node_id"`
		Token   string `json:"token"`
		JobID   string `json:"job_id"`
		OK      bool   `json:"ok"`
		Latency int64  `json:"latency_ms"`
		Error   string `json:"error"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if _, err := s.store.GetNodeByToken(req.NodeID, req.Token); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	s.completeDial(req.NodeID, req.JobID, dialResult{OK: req.OK, Latency: req.Latency, Error: req.Error})
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// requestAgentDial queues a TCP dial job for the agent and waits for the result.
// Agent picks it up on next heartbeat (agents heartbeat every few seconds).
func (s *Server) requestAgentDial(nodeID, host string, port int, wait time.Duration) (ok bool, latencyMs int64, errMsg string) {
	if nodeID == "" || host == "" || port <= 0 {
		return false, 0, "invalid dial target"
	}
	jobID := strings.ReplaceAll(uuid.NewString(), "-", "")
	ch := make(chan dialResult, 1)

	s.dialMu.Lock()
	if s.dialWait[nodeID] == nil {
		s.dialWait[nodeID] = map[string]chan dialResult{}
	}
	s.dialWait[nodeID][jobID] = ch
	s.dialJobs[nodeID] = append(s.dialJobs[nodeID], dialJob{
		ID:      jobID,
		Host:    host,
		Port:    port,
		Timeout: 4000,
	})
	s.dialMu.Unlock()

	defer func() {
		s.dialMu.Lock()
		if m := s.dialWait[nodeID]; m != nil {
			delete(m, jobID)
			if len(m) == 0 {
				delete(s.dialWait, nodeID)
			}
		}
		// drop unconsumed job if still queued
		if jobs := s.dialJobs[nodeID]; len(jobs) > 0 {
			kept := jobs[:0]
			for _, j := range jobs {
				if j.ID != jobID {
					kept = append(kept, j)
				}
			}
			if len(kept) == 0 {
				delete(s.dialJobs, nodeID)
			} else {
				s.dialJobs[nodeID] = kept
			}
		}
		s.dialMu.Unlock()
	}()

	// Agents pick jobs on heartbeat (v0.3.4+: ~5s). Wait at least one cycle + dial + slack.
	if wait < 30*time.Second {
		wait = 30 * time.Second
	}
	select {
	case res := <-ch:
		if res.OK {
			return true, res.Latency, ""
		}
		msg := res.Error
		if msg == "" {
			msg = "dial failed"
		}
		return false, res.Latency, msg
	case <-time.After(wait):
		return false, 0, "等待 Agent 拨测超时（节点可能离线、Agent 过旧、或 apply 卡住未心跳；请升级 agent 到 v0.3.4+ 并看 journalctl -u mieru-agent）"
	}
}

func (s *Server) takeDialJobs(nodeID string) []dialJob {
	s.dialMu.Lock()
	defer s.dialMu.Unlock()
	jobs := s.dialJobs[nodeID]
	delete(s.dialJobs, nodeID)
	if jobs == nil {
		return []dialJob{}
	}
	return jobs
}

func (s *Server) completeDial(nodeID, jobID string, res dialResult) {
	s.dialMu.Lock()
	defer s.dialMu.Unlock()
	if m := s.dialWait[nodeID]; m != nil {
		if ch, ok := m[jobID]; ok {
			select {
			case ch <- res:
			default:
			}
		}
	}
}

func (s *Server) agentConfig(c *gin.Context) {
	nodeID := c.GetHeader("X-Node-ID")
	token := c.GetHeader("X-Node-Token")
	if nodeID == "" {
		nodeID = c.Query("node_id")
	}
	if token == "" {
		token = c.Query("token")
	}
	if nodeID == "" || token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
		return
	}
	if _, err := s.store.GetNodeByToken(nodeID, token); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	ver, raw, err := s.store.GetDesiredConfig(nodeID)
	if err != nil {
		_ = s.gen.RebuildAll()
		ver, raw, err = s.store.GetDesiredConfig(nodeID)
		if err != nil {
			c.JSON(http.StatusOK, model.AgentDesiredConfig{NodeID: nodeID, Version: 0, Plugins: []map[string]interface{}{}})
			return
		}
	}
	var cfg model.AgentDesiredConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "bad stored config"})
		return
	}
	cfg.Version = ver
	c.JSON(http.StatusOK, cfg)
}

func (s *Server) agentTraffic(c *gin.Context) {
	var req model.TrafficReport
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	n, err := s.store.GetNodeByToken(req.NodeID, req.Token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return
	}
	var applied, skipped int
	var sumUp, sumDown int64
	for _, sample := range req.Samples {
		if sample.UserID <= 0 {
			skipped++
			continue
		}
		_ = s.store.AddTraffic(sample.UserID, n.ID, sample.UpDelta, sample.DownDelta)
		ts := sample.TS
		if ts <= 0 {
			ts = time.Now().Unix()
		}
		s.store.SetRate(model.TrafficSample{
			UserID:    sample.UserID,
			UpBps:     sample.UpBps,
			DownBps:   sample.DownBps,
			UpBytes:   sample.UpDelta,
			DownBytes: sample.DownDelta,
			TS:        ts,
		})
		applied++
		sumUp += sample.UpDelta
		sumDown += sample.DownDelta
	}
	// If any user flipped to expired/over_quota, rebuild so exit mita drops them ASAP.
	if nChanged, err := s.store.RefreshUserStatusesChanged(); err == nil && nChanged > 0 {
		s.scheduleRebuild(fmt.Sprintf("quota_or_expire flipped=%d", nChanged))
	}
	s.trafficMu.Lock()
	s.lastTraffic[n.ID] = time.Now()
	s.trafficMu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true, "applied": applied, "skipped": skipped, "up": sumUp, "down": sumDown})
}

// ---------- Announcements ----------

func (s *Server) publicAnnouncements(c *gin.Context) {
	list, err := s.store.ListPublicAnnouncements()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	popup, err := s.store.PopupAnnouncement()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// strip internal flags noise is fine; still return enabled/popup for client
	items := make([]gin.H, 0, len(list))
	for _, a := range list {
		items = append(items, gin.H{
			"id":         a.ID,
			"title":      a.Title,
			"body":       a.Body,
			"popup":      a.Popup,
			"created_at": a.CreatedAt,
			"updated_at": a.UpdatedAt,
		})
	}
	var popupObj any
	if popup != nil {
		popupObj = gin.H{
			"id":         popup.ID,
			"title":      popup.Title,
			"body":       popup.Body,
			"popup":      true,
			"created_at": popup.CreatedAt,
			"updated_at": popup.UpdatedAt,
		}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"popup": popupObj,
	})
}

func (s *Server) listAnnouncements(c *gin.Context) {
	list, err := s.store.ListAnnouncements()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func (s *Server) createAnnouncement(c *gin.Context) {
	var req struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Enabled *bool  `json:"enabled"`
		Popup   bool   `json:"popup"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	en := true
	if req.Enabled != nil {
		en = *req.Enabled
	}
	a := &model.Announcement{
		Title:   req.Title,
		Body:    req.Body,
		Enabled: en,
		Popup:   req.Popup,
	}
	if err := s.store.CreateAnnouncement(a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "create_announcement", fmt.Sprintf("%d", a.ID), a.Title)
	c.JSON(http.StatusOK, a)
}

func (s *Server) updateAnnouncement(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Title   string `json:"title"`
		Body    string `json:"body"`
		Enabled *bool  `json:"enabled"`
		Popup   *bool  `json:"popup"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	cur, err := s.store.GetAnnouncement(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cur == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "公告不存在"})
		return
	}
	cur.Title = req.Title
	cur.Body = req.Body
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if req.Popup != nil {
		cur.Popup = *req.Popup
	}
	if err := s.store.UpdateAnnouncement(cur); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "update_announcement", fmt.Sprintf("%d", id), cur.Title)
	c.JSON(http.StatusOK, cur)
}

func (s *Server) deleteAnnouncement(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := s.store.DeleteAnnouncement(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "delete_announcement", fmt.Sprintf("%d", id), "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) setAnnouncementPopup(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Popup *bool `json:"popup"`
	}
	_ = c.ShouldBindJSON(&req)
	popup := true
	if req.Popup != nil {
		popup = *req.Popup
	}
	// enabling popup implies enabled=1 so query page can show it
	if popup {
		cur, err := s.store.GetAnnouncement(id)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if cur == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "公告不存在"})
			return
		}
		if !cur.Enabled {
			cur.Enabled = true
			cur.Popup = true
			if err := s.store.UpdateAnnouncement(cur); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			s.store.Audit("admin", "set_announcement_popup", fmt.Sprintf("%d", id), "on+enable")
			c.JSON(http.StatusOK, cur)
			return
		}
	}
	if err := s.store.SetAnnouncementPopup(id, popup); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	cur, _ := s.store.GetAnnouncement(id)
	s.store.Audit("admin", "set_announcement_popup", fmt.Sprintf("%d", id), map[bool]string{true: "on", false: "off"}[popup])
	if cur == nil {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}
	c.JSON(http.StatusOK, cur)
}

// ---------- Client files (query-page downloads) ----------

const maxClientFileSize = 500 << 20 // 500 MiB
// maxClientFileChunk keeps each request under typical nginx client_max_body_size 1m.
const maxClientFileChunk = 512 << 10 // 512 KiB

func (s *Server) clientFilesDir() string {
	dir := filepath.Join(s.cfg.DataDir, "client-files")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (s *Server) clientFilesTmpDir() string {
	dir := filepath.Join(s.clientFilesDir(), "tmp")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func (s *Server) purgeStaleUploadsLocked() {
	cutoff := time.Now().Add(-2 * time.Hour)
	for id, u := range s.pendingUpload {
		if u == nil || u.CreatedAt.Before(cutoff) {
			if u != nil && u.Path != "" {
				_ = os.Remove(u.Path)
			}
			delete(s.pendingUpload, id)
		}
	}
}

func (s *Server) takePendingUpload(id string) *pendingClientUpload {
	s.uploadMu.Lock()
	defer s.uploadMu.Unlock()
	s.purgeStaleUploadsLocked()
	return s.pendingUpload[id]
}

func (s *Server) initClientFileUpload(c *gin.Context) {
	var req struct {
		Filename    string `json:"filename"`
		Title       string `json:"title"`
		Size        int64  `json:"size"`
		ContentType string `json:"content_type"`
		Enabled     *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	name := filepath.Base(strings.TrimSpace(req.Filename))
	if name == "" || name == "." || name == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件名无效"})
		return
	}
	if req.Size <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件大小无效"})
		return
	}
	if req.Size > maxClientFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大（上限 500MB）"})
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = name
	}
	if len([]rune(title)) > 120 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "标题过长（最多 120 字）"})
		return
	}
	en := true
	if req.Enabled != nil {
		en = *req.Enabled
	}
	ct := strings.TrimSpace(req.ContentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	id := uuid.NewString()
	tmpPath := filepath.Join(s.clientFilesTmpDir(), id+".part")
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法创建临时文件: " + err.Error()})
		return
	}
	_ = f.Close()

	s.uploadMu.Lock()
	s.purgeStaleUploadsLocked()
	s.pendingUpload[id] = &pendingClientUpload{
		ID:          id,
		Filename:    name,
		Title:       title,
		ContentType: ct,
		Enabled:     en,
		Size:        req.Size,
		Received:    0,
		Path:        tmpPath,
		CreatedAt:   time.Now(),
	}
	s.uploadMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"upload_id":   id,
		"chunk_size":  maxClientFileChunk,
		"max_size":    maxClientFileSize,
		"filename":    name,
		"title":       title,
		"size":        req.Size,
	})
}

func (s *Server) chunkClientFileUpload(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload id"})
		return
	}
	offStr := c.Query("offset")
	offset, err := strconv.ParseInt(offStr, 10, 64)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
		return
	}
	sess := s.takePendingUpload(id)
	if sess == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "上传会话不存在或已过期，请重新上传"})
		return
	}
	// Body limit slightly above chunk size for safety.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxClientFileChunk+64*1024)

	s.uploadMu.Lock()
	cur := s.pendingUpload[id]
	if cur == nil {
		s.uploadMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "上传会话不存在或已过期，请重新上传"})
		return
	}
	if offset != cur.Received {
		got := cur.Received
		s.uploadMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "offset 不匹配", "received": got, "expected_offset": got})
		return
	}
	if offset >= cur.Size {
		s.uploadMu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件已传完"})
		return
	}
	remain := cur.Size - offset
	limit := int64(maxClientFileChunk)
	if remain < limit {
		limit = remain
	}
	path := cur.Path
	s.uploadMu.Unlock()

	f, err := os.OpenFile(path, os.O_WRONLY, 0o644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法打开临时文件: " + err.Error()})
		return
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		_ = f.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "seek 失败: " + err.Error()})
		return
	}
	n, copyErr := io.Copy(f, io.LimitReader(c.Request.Body, limit+1))
	_ = f.Close()
	if copyErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "读取分片失败: " + copyErr.Error()})
		return
	}
	if n > limit {
		c.JSON(http.StatusBadRequest, gin.H{"error": "分片过大"})
		return
	}
	if n == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "空分片"})
		return
	}

	s.uploadMu.Lock()
	cur = s.pendingUpload[id]
	if cur == nil {
		s.uploadMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "上传会话不存在或已过期，请重新上传"})
		return
	}
	if offset != cur.Received {
		got := cur.Received
		s.uploadMu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "offset 不匹配", "received": got})
		return
	}
	cur.Received += n
	received := cur.Received
	total := cur.Size
	s.uploadMu.Unlock()

	c.JSON(http.StatusOK, gin.H{
		"received": received,
		"size":     total,
		"done":     received >= total,
	})
}

func (s *Server) completeClientFileUpload(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid upload id"})
		return
	}
	s.uploadMu.Lock()
	sess := s.pendingUpload[id]
	if sess == nil {
		s.uploadMu.Unlock()
		c.JSON(http.StatusNotFound, gin.H{"error": "上传会话不存在或已过期，请重新上传"})
		return
	}
	if sess.Received != sess.Size {
		got, want := sess.Received, sess.Size
		s.uploadMu.Unlock()
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("文件未传完（%d/%d）", got, want)})
		return
	}
	delete(s.pendingUpload, id)
	s.uploadMu.Unlock()

	// move temp -> final uuid name
	stored := uuid.NewString()
	dest := filepath.Join(s.clientFilesDir(), stored)
	if err := os.Rename(sess.Path, dest); err != nil {
		// cross-device fallback
		if err2 := copyFile(sess.Path, dest); err2 != nil {
			_ = os.Remove(sess.Path)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "保存文件失败: " + err2.Error()})
			return
		}
		_ = os.Remove(sess.Path)
	}
	// verify size
	if st, err := os.Stat(dest); err != nil || st.Size() != sess.Size {
		_ = os.Remove(dest)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "文件大小校验失败"})
		return
	}
	rec := &model.ClientFile{
		Title:       sess.Title,
		Filename:    sess.Filename,
		StoredName:  stored,
		Size:        sess.Size,
		ContentType: sess.ContentType,
		Enabled:     sess.Enabled,
	}
	if err := s.store.CreateClientFile(rec); err != nil {
		_ = os.Remove(dest)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "upload_client_file", fmt.Sprintf("%d", rec.ID), sess.Filename+" (chunked)")
	c.JSON(http.StatusOK, rec)
}

func (s *Server) abortClientFileUpload(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	s.uploadMu.Lock()
	sess := s.pendingUpload[id]
	delete(s.pendingUpload, id)
	s.uploadMu.Unlock()
	if sess != nil && sess.Path != "" {
		_ = os.Remove(sess.Path)
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *Server) publicClientFiles(c *gin.Context) {
	list, err := s.store.ListPublicClientFiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	items := make([]gin.H, 0, len(list))
	for _, f := range list {
		items = append(items, gin.H{
			"id":           f.ID,
			"title":        f.Title,
			"filename":     f.Filename,
			"size":         f.Size,
			"content_type": f.ContentType,
			"created_at":   f.CreatedAt,
			"updated_at":   f.UpdatedAt,
			"download_url": fmt.Sprintf("/api/files/%d/download", f.ID),
		})
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) publicDownloadClientFile(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	f, err := s.store.GetClientFile(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if f == nil || !f.Enabled {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	pathOnDisk := filepath.Join(s.clientFilesDir(), f.StoredName)
	// prevent path escape even though stored name is uuid
	if !strings.HasPrefix(filepath.Clean(pathOnDisk), filepath.Clean(s.clientFilesDir())+string(os.PathSeparator)) &&
		filepath.Clean(pathOnDisk) != filepath.Clean(s.clientFilesDir()) {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	if st, err := os.Stat(pathOnDisk); err != nil || st.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	name := f.Filename
	if name == "" {
		name = f.Title
	}
	if name == "" {
		name = "download"
	}
	c.Header("Cache-Control", "no-store")
	c.FileAttachment(pathOnDisk, name)
}

func (s *Server) listClientFiles(c *gin.Context) {
	list, err := s.store.ListClientFiles()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func (s *Server) uploadClientFile(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxClientFileSize+1<<20)
	file, hdr, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择文件（字段名 file）"})
		return
	}
	defer file.Close()
	if hdr.Size > maxClientFileSize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大（上限 500MB）"})
		return
	}
	origName := filepath.Base(strings.TrimSpace(hdr.Filename))
	if origName == "" || origName == "." || origName == ".." {
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件名无效"})
		return
	}
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		title = origName
	}
	en := true
	if v := strings.TrimSpace(c.PostForm("enabled")); v == "0" || strings.EqualFold(v, "false") {
		en = false
	}
	ct := hdr.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	stored := uuid.NewString()
	dir := s.clientFilesDir()
	dest := filepath.Join(dir, stored)
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "无法写入文件: " + err.Error()})
		return
	}
	n, copyErr := io.Copy(out, io.LimitReader(file, maxClientFileSize+1))
	_ = out.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "写入失败: " + copyErr.Error()})
		return
	}
	if n > maxClientFileSize {
		_ = os.Remove(dest)
		c.JSON(http.StatusBadRequest, gin.H{"error": "文件过大（上限 500MB）"})
		return
	}
	rec := &model.ClientFile{
		Title:       title,
		Filename:    origName,
		StoredName:  stored,
		Size:        n,
		ContentType: ct,
		Enabled:     en,
	}
	if err := s.store.CreateClientFile(rec); err != nil {
		_ = os.Remove(dest)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "upload_client_file", fmt.Sprintf("%d", rec.ID), origName)
	c.JSON(http.StatusOK, rec)
}

func (s *Server) updateClientFile(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	var req struct {
		Title   string `json:"title"`
		Enabled *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	cur, err := s.store.GetClientFile(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cur == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "文件不存在"})
		return
	}
	if strings.TrimSpace(req.Title) != "" {
		cur.Title = req.Title
	}
	if req.Enabled != nil {
		cur.Enabled = *req.Enabled
	}
	if err := s.store.UpdateClientFile(cur); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "update_client_file", fmt.Sprintf("%d", id), cur.Title)
	c.JSON(http.StatusOK, cur)
}

func (s *Server) deleteClientFile(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	stored, err := s.store.DeleteClientFile(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if stored != "" {
		_ = os.Remove(filepath.Join(s.clientFilesDir(), filepath.Base(stored)))
	}
	s.store.Audit("admin", "delete_client_file", fmt.Sprintf("%d", id), stored)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
