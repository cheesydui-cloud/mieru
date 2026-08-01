package api

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/auth"
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
}

func New(cfg config.PanelConfig, st *store.Store) *Server {
	return &Server{
		cfg:         cfg,
		store:       st,
		jwt:         auth.NewTokenManager(cfg.JWTSecret),
		gen:         &configgen.Builder{Store: st},
		Version:     "dev",
		dialWait:    map[string]map[string]chan dialResult{},
		dialJobs:    map[string][]dialJob{},
		upgradeJobs: map[string]*upgradeJob{},
	}
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
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

	r.GET("/sub/:token", s.subscription)
	r.GET("/api/sub/:token", s.subscription)
	// Mihomo / Clash Meta: import as remote config or download file
	r.GET("/sub/:token/mihomo.yaml", s.subscriptionMihomo)
	r.GET("/api/sub/:token/mihomo.yaml", s.subscriptionMihomo)
	r.POST("/api/auth/login", s.login)

		agent := r.Group("/api/agent")
		{
			agent.POST("/heartbeat", s.agentHeartbeat)
			agent.GET("/config", s.agentConfig)
			agent.POST("/traffic", s.agentTraffic)
			agent.POST("/dial-result", s.agentDialResult)
			agent.POST("/upgrade-result", s.agentUpgradeResult)
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

		admin.GET("/metrics/rates", s.listRates)
		admin.GET("/audit", s.listAudit)

		admin.GET("/settings", s.getSettings)
		admin.PUT("/settings", s.putSettings)
		admin.POST("/admin-password", s.changeAdminPassword)
		admin.GET("/nodes/:id/install", s.nodeInstallCmd)
		admin.GET("/diagnose", s.diagnose)
		admin.GET("/nodes/:id/desired", s.nodeDesiredConfig)
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

func (s *Server) login(c *gin.Context) {
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
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if adm, err := s.store.GetAdminByUsername(req.Username); err == nil {
		if store.CheckPassword(adm.PasswordHash, req.Password) {
			tok, err := s.jwt.Issue(fmt.Sprintf("%d", adm.ID), "admin", adm.Username)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "token issue failed"})
				return
			}
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
			c.JSON(http.StatusOK, gin.H{"token": tok, "role": "user", "username": u.Username})
			return
		}
	}
	c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
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
			AgentToken     string `json:"agent_token,omitempty"`
			AgentVersion   string `json:"agent_version,omitempty"`
			ApplyError     string `json:"apply_error,omitempty"`
			UpgradeStatus  string `json:"upgrade_status,omitempty"`  // pending|running|ok|error
			UpgradeTarget  string `json:"upgrade_target,omitempty"`  // target version
			UpgradeError   string `json:"upgrade_error,omitempty"`
			UpgradePending bool   `json:"upgrade_pending,omitempty"` // still in panel queue
			PanelVersion   string `json:"panel_version,omitempty"`
		}
		out := make([]nodeOut, 0, len(list))
		reveal := c.Query("reveal") == "1"
		panelVer := strings.TrimPrefix(strings.TrimSpace(s.Version), "v")
		for _, n := range list {
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
			if reveal {
				if full, err := s.store.GetNode(n.ID); err == nil {
					no.AgentToken = full.AgentToken
				}
			}
			out = append(out, no)
		}
		c.JSON(http.StatusOK, out)
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
	if err := s.store.DeleteNode(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "delete_node", id, "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
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

func (s *Server) rebuildAll(c *gin.Context) {
	if err := s.gen.RebuildAll(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "rebuild_all", "*", "")
	// surface backbone (username only) so ops can confirm tunnel identity
	bbUser, _ := s.store.GetSetting(configgen.SettingBackboneUser)
	c.JSON(http.StatusOK, gin.H{"ok": true, "backbone_user": bbUser})
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
	routes, _ := s.store.ListRoutes()
	bbUser, _ := s.store.GetSetting(configgen.SettingBackboneUser)
	bbPass, _ := s.store.GetSetting(configgen.SettingBackbonePass)

	type nodeDiag struct {
		ID            string                   `json:"id"`
		Name          string                   `json:"name"`
		Role          string                   `json:"role"`
		Status        string                   `json:"status"`
		PublicIP      string                   `json:"public_ip"`
		PrivateIP     string                   `json:"private_ip"`
		DialHost      string                   `json:"dial_host"`
		PublicPort    int                      `json:"public_port"`
		MitaPort      int                      `json:"mita_port,omitempty"`
		ConfigVersion int64                    `json:"config_version"`
		Plugins       []map[string]interface{} `json:"plugins"`
		UserCount     int                      `json:"user_count"`
		Issues        []string                 `json:"issues"`
	}
	out := make([]nodeDiag, 0, len(nodes))
	globalIssues := []string{}
	if len(users) == 0 {
		globalIssues = append(globalIssues, "no active proxy users — socks_in/mita will refuse to start")
	}
	if bbUser == "" || bbPass == "" {
		globalIssues = append(globalIssues, "backbone credentials missing — click 重建配置 to generate")
	}
	enabledRoutes := 0
	for _, r := range routes {
		if r.Enabled {
			enabledRoutes++
		}
	}
	if enabledRoutes == 0 {
		globalIssues = append(globalIssues, "no enabled routes — entry/relay upstreams may be empty")
	}

	for _, n := range nodes {
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
		}
		if n.Role == model.RoleExit || n.Role == model.RoleHybrid {
			d.MitaPort = n.MitaPrimaryPort()
		}
		if n.DialHost() == "" {
			d.Issues = append(d.Issues, "no public_ip / private_ip / hostname — previous hop cannot dial")
		}
		if n.Status != model.StatusOnline && n.Status != model.StatusDegraded {
			d.Issues = append(d.Issues, "agent offline or never heartbeated")
		}
		if n.Status == model.StatusDegraded {
			d.Issues = append(d.Issues, "agent reports degraded (last apply failed — check journalctl -u mieru-agent)")
		}
		_, raw, err := s.store.GetDesiredConfig(n.ID)
		if err != nil || raw == "" {
			d.Issues = append(d.Issues, "no desired config — rebuild needed")
		} else {
			var cfg model.AgentDesiredConfig
			if json.Unmarshal([]byte(raw), &cfg) == nil {
				d.UserCount = len(cfg.Users)
				// redact plugin configs to host/port/type only
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
				// role-specific checks (v0.4 product path: front tcp_forward → exit mita)
				has := map[string]bool{}
				for _, p := range cfg.Plugins {
					t, _ := p["type"].(string)
					has[t] = true
				}
				switch n.Role {
				case model.RoleExit:
					if !has["mita_server"] {
						d.Issues = append(d.Issues, "缺少 mita_server（落地未配置）")
					}
					if d.UserCount == 0 {
						d.Issues = append(d.Issues, "mita 用户数为 0")
					}
				case model.RoleRelay, model.RoleEntry:
					// Preferred: transparent tcp_forward to exit mita (mierus on front IP).
					if has["tcp_forward"] {
						// ok for multi-hop front
					} else if has["socks_in"] {
						// legacy socks chain — warn but not hard fail
						d.Issues = append(d.Issues, "仍是 socks_in 链式（建议绑线路后重建为 tcp_forward）")
					} else {
						d.Issues = append(d.Issues, "缺少 tcp_forward（前置未指向落地，请建线路并重建配置）")
					}
				case model.RoleHybrid:
					if !has["mita_server"] {
						d.Issues = append(d.Issues, "hybrid 缺少 mita_server")
					}
					if !has["mieru_client"] && !has["tcp_forward"] {
						d.Issues = append(d.Issues, "hybrid 缺少 mieru_client")
					}
					if !has["socks_in"] && !has["tcp_forward"] {
						d.Issues = append(d.Issues, "hybrid 缺少对外监听（socks_in）")
					}
				}
			}
		}
		out = append(out, d)
	}

	c.JSON(http.StatusOK, gin.H{
		"version":        s.Version,
		"backbone_user":  bbUser,
		"backbone_set":   bbUser != "" && bbPass != "",
		"active_users":   len(users),
		"enabled_routes": enabledRoutes,
		"global_issues":  globalIssues,
		"nodes":          out,
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
	FrontPort int    `json:"front_port,omitempty"` // 前置入口端口（手机扫码连这个）
	ExitPort  int    `json:"exit_port,omitempty"`  // 落地 mita 端口
	FrontHost string `json:"front_host,omitempty"`
	ExitHost  string `json:"exit_host,omitempty"`
	FrontName string `json:"front_name,omitempty"`
	ExitName  string `json:"exit_name,omitempty"`
}

func (s *Server) listRoutes(c *gin.Context) {
	list, err := s.store.ListRoutes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]routeView, 0, len(list))
	for i := range list {
		out = append(out, s.enrichRoute(&list[i]))
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
				// Client-facing advertise: public IP / hostname preferred over private DialHost
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
	// Per-route front listen port (multi-exit pool allocation, same as share/QR).
	if frontID != "" {
		if p := configgen.FrontListenPort(s.store, frontID, r); p > 0 {
			v.FrontPort = p
		} else if n, err := s.store.GetNode(frontID); err == nil {
			v.FrontPort = n.PublicServicePort()
		}
	}
	return v
}

// validateRouteFrontPorts checks hop.Port on front hops is within the node's
// port pool and not already claimed by another enabled route on the same front.
// excludeRouteID is the route being updated (0 for create).
//
// Occupied ports are taken from the same allocator as configgen (FrontListenPort),
// so auto-assigned tunnels (no hop.port pin) still block re-use. Error text names
// the tunnel that holds the port.
func (s *Server) validateRouteFrontPorts(hopsJSON string, excludeRouteID int64) error {
	var hops []model.Hop
	if err := json.Unmarshal([]byte(hopsJSON), &hops); err != nil {
		return fmt.Errorf("hops_json 无效")
	}
	others, _ := s.store.ListRoutes()
	type claim struct {
		routeID int64
		name    string
	}
	// frontID → port → claim (one pass over all other enabled routes)
	used := map[string]map[int]claim{}
	// Per front, compute all allocated ports once via frontForwards semantics.
	frontSeen := map[string]bool{}
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
			if n.Role != model.RoleRelay && n.Role != model.RoleEntry {
				continue
			}
			if frontSeen[n.ID] {
				// still need this route's port below
			}
			frontSeen[n.ID] = true
			port := h.Port
			if port <= 0 {
				port = configgen.FrontListenPort(s.store, n.ID, or)
			}
			if port <= 0 {
				continue
			}
			if used[n.ID] == nil {
				used[n.ID] = map[int]claim{}
			}
			// Prefer first claim (lower route id typically); keep name for error.
			if _, exists := used[n.ID][port]; !exists {
				used[n.ID][port] = claim{routeID: or.ID, name: or.Name}
			}
		}
	}

	for _, h := range hops {
		if h.NodeID == "" || h.External || h.Port <= 0 {
			continue
		}
		n, err := s.store.GetNode(h.NodeID)
		if err != nil {
			return fmt.Errorf("前置节点不存在: %s", h.NodeID)
		}
		if n.Role != model.RoleRelay && n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
			continue
		}
		pmin, pmax := n.EffectivePortRange()
		if h.Port < pmin || h.Port > pmax {
			return fmt.Errorf("入口端口 %d 不在前置 %s 的端口池 %d–%d 内", h.Port, n.Name, pmin, pmax)
		}
		if m := used[n.ID]; m != nil {
			if c, ok := m[h.Port]; ok {
				return fmt.Errorf("入口端口 %d 已被隧道「%s」(#%d) 占用，请换端口、删掉该隧道，或留空自动分配", h.Port, c.name, c.routeID)
			}
		}
	}
	return nil
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
	if err := s.validateRouteFrontPorts(req.HopsJSON, 0); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
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
	if err := s.validateRouteFrontPorts(req.HopsJSON, id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	r.Name = req.Name
	r.Enabled = req.Enabled
	r.Strategy = req.Strategy
	r.HopsJSON = req.HopsJSON
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
		legCount := 0      // legs that were actually dial-tested
		skipCount := 0     // external / no-agent informational skips

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

		c.JSON(http.StatusOK, gin.H{
			"route_id":    id,
			"health":      health,
			"hops":        results,
			"checked_at":  time.Now().UTC().Format(time.RFC3339),
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
		RouteName    string `json:"route_name,omitempty"`
		EntryDisplay string `json:"entry_display,omitempty"`
	}
	out := make([]row, 0, len(list))
	for _, u := range list {
		r := row{User: u, Subscription: base + "/sub/" + u.SubToken}
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
			// stale (>8s) → show 0 so UI never freezes on last speed
			if sample.TS > 0 && now-sample.TS > 8 {
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
			"user":           u,
			"proxy_password": u.ProxyPassword,
			"sub_token":      u.SubToken,
			"subscription":   base + "/sub/" + u.SubToken,
			"share_url":      share["share_url"],
			"share_urls":     share["share_urls"],
			"entries":        share["entries"],
			"mihomo_yaml":    share["mihomo_yaml"],
			"mihomo_url":     base + "/sub/" + u.SubToken + "/mihomo.yaml",
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
			"user":         u,
			"rate":         sample,
			"subscription": base + "/sub/" + u.SubToken,
			"share_url":    share["share_url"],
			"share_urls":   share["share_urls"],
			"entries":      share["entries"],
			"mihomo_yaml":  share["mihomo_yaml"],
			"mihomo_url":   base + "/sub/" + u.SubToken + "/mihomo.yaml",
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
	if err := s.store.DeleteUser(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) resetUserPassword(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	pw, err := s.store.ResetUserProxyPassword(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	c.JSON(http.StatusOK, gin.H{"proxy_password": pw})
}

func (s *Server) resetUserSub(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	tok, err := s.store.ResetSubToken(id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sub_token": tok, "subscription": s.publicBase(c) + "/sub/" + tok})
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

func (s *Server) listRates(c *gin.Context) {
	// drop stale samples (>8s) so UI doesn't show frozen speeds
	rates := s.store.AllRates()
	now := time.Now().Unix()
	out := make([]model.TrafficSample, 0, len(rates))
	for _, r := range rates {
		if r.TS > 0 && now-r.TS > 8 {
			r.UpBps = 0
			r.DownBps = 0
		}
		out = append(out, r)
	}
	c.JSON(http.StatusOK, out)
}

func (s *Server) listAudit(c *gin.Context) {
	list, err := s.store.ListAudit(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
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

func (s *Server) buildInstallCmd(c *gin.Context, n *model.Node) installInfo {
	base := s.publicBase(c)
	role := n.Role
	if role == "" {
		role = "exit"
	}
	cmd := fmt.Sprintf(
		"curl -fsSL https://raw.githubusercontent.com/cheesydui-cloud/mieru/main/scripts/install-agent.sh | bash -s -- --panel-url %s --node-id %s --token %s --role %s",
		base, n.ID, n.AgentToken, role,
	)
	hint := "在对应 Linux 节点上执行上述命令。请先在「设置」填写面板地址（外网可访问的 http/https）。"
	if s.store.PanelBaseURL() == "" {
		hint = "尚未配置面板地址，当前用浏览器访问地址生成命令。生产环境请到「设置」填写固定面板地址。"
	}
	return installInfo{PanelURL: base, Cmd: cmd, Hint: hint}
}

func (s *Server) getSettings(c *gin.Context) {
	m, err := s.store.GetSettings("panel_url", "panel_name", configgen.SettingBackboneUser)
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
		name = "Mieru Panel"
	}
	c.JSON(http.StatusOK, gin.H{
		"panel_url":      panelURL,
		"panel_name":     name,
		"panel_url_set":  m["panel_url"] != "",
		"version":        s.Version,
		"admin_user":     s.cfg.AdminUser,
		"backbone_user":  m[configgen.SettingBackboneUser],
		"backbone_ready": m[configgen.SettingBackboneUser] != "",
	})
}

func (s *Server) putSettings(c *gin.Context) {
	var req struct {
		PanelURL  string `json:"panel_url"`
		PanelName string `json:"panel_name"`
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
	if name == "" {
		name = "Mieru Panel"
	}
	if err := s.store.SetSetting("panel_name", name); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "update_settings", "panel", url)
	c.JSON(http.StatusOK, gin.H{"ok": true, "panel_url": url, "panel_name": name})
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
func buildMihomoYAML(u *model.User, endpoints []shareEndpoint) string {
	var b strings.Builder
	b.WriteString("# Mihomo / Clash Meta — generated by mieru-panel\n")
	b.WriteString("# user: " + u.Username + "\n")
	b.WriteString("# path: phone → front (mieru) → residential mita egress\n")
	b.WriteString("# Import: Profiles → Import from file / paste YAML\n")
	b.WriteString("#\n")
	b.WriteString("mixed-port: 7890\n")
	b.WriteString("allow-lan: false\n")
	b.WriteString("mode: rule\n")
	b.WriteString("log-level: info\n")
	b.WriteString("ipv6: false\n")
	b.WriteString("\n")
	b.WriteString("dns:\n")
	b.WriteString("  enable: true\n")
	b.WriteString("  enhanced-mode: fake-ip\n")
	b.WriteString("  nameserver:\n")
	b.WriteString("    - 8.8.8.8\n")
	b.WriteString("    - 1.1.1.1\n")
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
	b.WriteString("rules:\n")
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
	c.Header("Content-Disposition", "attachment; filename="+fname)
	c.Header("Profile-Update-Interval", "24")
	c.Data(http.StatusOK, "application/x-yaml; charset=utf-8", []byte(body))
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
		"agent_version": strings.TrimSpace(req.AgentVersion),
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
	for _, sample := range req.Samples {
		_ = s.store.AddTraffic(sample.UserID, n.ID, sample.UpDelta, sample.DownDelta)
		s.store.SetRate(model.TrafficSample{
			UserID:    sample.UserID,
			UpBps:     sample.UpBps,
			DownBps:   sample.DownBps,
			UpBytes:   sample.UpDelta,
			DownBytes: sample.DownDelta,
			TS:        sample.TS,
		})
	}
	_ = s.store.RefreshUserStatuses()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
