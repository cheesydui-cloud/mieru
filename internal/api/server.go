package api

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/auth"
	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/configgen"
	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
	"github.com/cheesydui-cloud/mieru/web"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

type Server struct {
	cfg     config.PanelConfig
	store   *store.Store
	jwt     *auth.TokenManager
	gen     *configgen.Builder
	Version string
}

func New(cfg config.PanelConfig, st *store.Store) *Server {
	return &Server{
		cfg:     cfg,
		store:   st,
		jwt:     auth.NewTokenManager(cfg.JWTSecret),
		gen:     &configgen.Builder{Store: st},
		Version: "dev",
	}
}

func (s *Server) Router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), gin.Logger())

	c := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	if len(s.cfg.CORSOrigins) == 1 && s.cfg.CORSOrigins[0] == "*" {
		c.AllowAllOrigins = true
	} else {
		c.AllowOrigins = s.cfg.CORSOrigins
	}
	r.Use(cors.New(c))

	r.GET("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "ts": time.Now().UTC(), "version": s.Version})
	})
	r.GET("/api/version", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"version": s.Version, "ui": "embedded"})
	})

	r.GET("/sub/:token", s.subscription)
	r.GET("/api/sub/:token", s.subscription)
	r.POST("/api/auth/login", s.login)

	agent := r.Group("/api/agent")
	{
		agent.POST("/heartbeat", s.agentHeartbeat)
		agent.GET("/config", s.agentConfig)
		agent.POST("/traffic", s.agentTraffic)
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
		admin.POST("/rebuild", s.rebuildAll)

		admin.GET("/routes", s.listRoutes)
		admin.POST("/routes", s.createRoute)
		admin.PUT("/routes/:id", s.updateRoute)
		admin.DELETE("/routes/:id", s.deleteRoute)
		admin.POST("/routes/:id/probe", s.probeRoute)

		admin.GET("/users", s.listUsers)
		admin.POST("/users", s.createUser)
		admin.GET("/users/:id", s.getUser)
		admin.PUT("/users/:id", s.updateUser)
		admin.DELETE("/users/:id", s.deleteUser)
		admin.POST("/users/:id/reset-password", s.resetUserPassword)
		admin.POST("/users/:id/reset-sub", s.resetUserSub)

		admin.GET("/metrics/rates", s.listRates)
			admin.GET("/audit", s.listAudit)

			admin.GET("/settings", s.getSettings)
			admin.PUT("/settings", s.putSettings)
			admin.POST("/admin-password", s.changeAdminPassword)
			admin.GET("/nodes/:id/install", s.nodeInstallCmd)
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
	// 1) embedded
	if f, err := dist.Open(rel); err == nil {
		defer f.Close()
		stat, err := f.Stat()
		if err == nil && !stat.IsDir() {
			if rs, ok := f.(io.ReadSeeker); ok {
				http.ServeContent(c.Writer, c.Request, path.Base(rel), stat.ModTime(), rs)
				return true
			}
			data, err := io.ReadAll(f)
			if err == nil {
				c.Data(http.StatusOK, contentType(rel), data)
				return true
			}
		}
	}
	// 2) on-disk fallback for local dev
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
			tok, _ := s.jwt.Issue(fmt.Sprintf("%d", adm.ID), "admin", adm.Username)
			c.JSON(http.StatusOK, gin.H{"token": tok, "role": "admin", "username": adm.Username})
			return
		}
	}
	if u, err := s.store.GetUserByUsername(req.Username); err == nil {
		if u.ProxyPassword == req.Password || store.CheckPassword(u.PasswordHash, req.Password) {
			tok, _ := s.jwt.Issue(fmt.Sprintf("%d", u.ID), "user", u.Username)
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
		AgentToken string `json:"agent_token,omitempty"`
	}
	out := make([]nodeOut, 0, len(list))
	reveal := c.Query("reveal") == "1"
	for _, n := range list {
		no := nodeOut{Node: n}
		no.AgentToken = ""
		no.Node.AgentToken = ""
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

func (s *Server) rebuildAll(c *gin.Context) {
	if err := s.gen.RebuildAll(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	s.store.Audit("admin", "rebuild_all", "*", "")
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) listRoutes(c *gin.Context) {
	list, err := s.store.ListRoutes()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
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
	if err := s.store.CreateRoute(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "create_route", fmt.Sprintf("%d", req.ID), req.Name)
	c.JSON(http.StatusCreated, req)
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
	c.JSON(http.StatusOK, r)
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

// probeRoute checks TCP reachability of each hop endpoint from the panel host
// and updates route.health. This is connectivity (port open), not full proxy auth.
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
		Kind    string `json:"kind"` // external|node
		Host    string `json:"host"`
		Port    int    `json:"port"`
		OK      bool   `json:"ok"`
		Latency int64  `json:"latency_ms"`
		Error   string `json:"error,omitempty"`
		NodeID  string `json:"node_id,omitempty"`
		Status  string `json:"agent_status,omitempty"`
	}
	results := make([]hopResult, 0, len(hops))
	allOK := len(hops) > 0
	anyOK := false

	for _, h := range hops {
		hr := hopResult{}
		if h.External || (h.NodeID == "" && h.Host != "") {
			hr.Kind = "external"
			hr.Host = strings.TrimSpace(h.Host)
			hr.Port = h.Port
			if hr.Port <= 0 {
				hr.Port = 1080
			}
			hr.Label = h.Name
			if hr.Label == "" {
				hr.Label = hr.Host
			}
		} else if h.NodeID != "" {
			hr.Kind = "node"
			hr.NodeID = h.NodeID
			n, err := s.store.GetNode(h.NodeID)
			if err != nil {
				hr.OK = false
				hr.Error = "node not found"
				hr.Label = h.NodeID
				results = append(results, hr)
				allOK = false
				continue
			}
			hr.Label = n.Name
			hr.Status = n.Status
			host := n.Hostname
			if host == "" {
				host = n.PublicIP
			}
			// prefer IP for panel→node probe when both set
			if n.PublicIP != "" {
				host = n.PublicIP
			}
			hr.Host = host
			hr.Port = n.EffectiveListenPort()
			if h.Port > 0 {
				hr.Port = h.Port
			}
		} else {
			hr.OK = false
			hr.Error = "empty hop"
			hr.Label = "?"
			results = append(results, hr)
			allOK = false
			continue
		}

		if hr.Host == "" || hr.Port <= 0 {
			hr.OK = false
			hr.Error = "missing host/port"
			results = append(results, hr)
			allOK = false
			continue
		}

		addr := net.JoinHostPort(hr.Host, strconv.Itoa(hr.Port))
		start := time.Now()
		conn, err := net.DialTimeout("tcp", addr, 4*time.Second)
		hr.Latency = time.Since(start).Milliseconds()
		if err != nil {
			hr.OK = false
			hr.Error = err.Error()
			allOK = false
		} else {
			_ = conn.Close()
			hr.OK = true
			anyOK = true
		}
		results = append(results, hr)
	}

	health := "unknown"
	if len(hops) == 0 {
		health = "unknown"
		allOK = false
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
		"route_id": id,
		"health":   health,
		"hops":     results,
		"checked_at": time.Now().UTC().Format(time.RFC3339),
		"note":     "TCP connect from panel host; not full proxy authentication",
	})
}

func (s *Server) listUsers(c *gin.Context) {
	_ = s.store.RefreshUserStatuses()
	list, err := s.store.ListUsers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type row struct {
		model.User
		UpBps   int64 `json:"up_bps"`
		DownBps int64 `json:"down_bps"`
	}
	out := make([]row, 0, len(list))
	for _, u := range list {
		r := row{User: u}
		if sample, ok := s.store.GetRate(u.ID); ok {
			r.UpBps = sample.UpBps
			r.DownBps = sample.DownBps
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
		Note              string `json:"note"`
		ProxyPassword     string `json:"proxy_password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if req.Username == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username required"})
		return
	}
	u := &model.User{
		Username:          req.Username,
		TrafficLimitBytes: req.TrafficLimitBytes,
		SpeedLimitBps:     req.SpeedLimitBps,
		MaxSessions:       req.MaxSessions,
		StickyExitID:      req.StickyExitID,
		RouteID:           req.RouteID,
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = s.gen.RebuildAll()
	s.store.Audit("admin", "create_user", fmt.Sprintf("%d", u.ID), u.Username)
	base := s.publicBase(c)
	c.JSON(http.StatusCreated, gin.H{
		"user":           u,
		"proxy_password": u.ProxyPassword,
		"sub_token":      u.SubToken,
		"subscription":   base + "/sub/" + u.SubToken,
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
	c.JSON(http.StatusOK, gin.H{"user": u, "rate": sample, "subscription": s.publicBase(c) + "/sub/" + u.SubToken})
}

func (s *Server) updateUser(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	u, err := s.store.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Status            string `json:"status"`
		ExpireAt          string `json:"expire_at"`
		TrafficLimitBytes *int64 `json:"traffic_limit_bytes"`
		SpeedLimitBps     *int64 `json:"speed_limit_bps"`
		MaxSessions       *int   `json:"max_sessions"`
		StickyExitID      string `json:"sticky_exit_id"`
		RouteID           *int64 `json:"route_id"`
		Note              string `json:"note"`
		ClearExpire       bool   `json:"clear_expire"`
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
		u.RouteID = req.RouteID
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

func (s *Server) listRates(c *gin.Context) {
	c.JSON(http.StatusOK, s.store.AllRates())
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
			host := n.Hostname
			if host == "" {
				host = n.PublicIP
			}
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
	m, err := s.store.GetSettings("panel_url", "panel_name")
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
		"panel_url":     panelURL,
		"panel_name":    name,
		"panel_url_set": m["panel_url"] != "",
		"version":       s.Version,
		"admin_user":    s.cfg.AdminUser,
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

		type entryProxy struct {
			Name string
			Host string
			Port int
		}
		seen := map[string]bool{}
		proxies := []entryProxy{}

		// Prefer entry endpoints from the user's bound route (supports external entry).
		if u.RouteID != nil {
			if r, err := s.store.GetRoute(*u.RouteID); err == nil && r.Enabled {
				var hops []model.Hop
				_ = json.Unmarshal([]byte(r.HopsJSON), &hops)
				for _, h := range hops {
					if h.External || (h.NodeID == "" && h.Host != "") {
						host := strings.TrimSpace(h.Host)
						if host == "" {
							continue
						}
						port := h.Port
						if port <= 0 {
							port = 1080
						}
						name := h.Name
						if name == "" {
							name = host
						}
						key := fmt.Sprintf("%s:%d", host, port)
						if seen[key] {
							continue
						}
						seen[key] = true
						proxies = append(proxies, entryProxy{Name: name, Host: host, Port: port})
						// only first external/entry hop is the client entry
						break
					}
					if h.NodeID == "" {
						continue
					}
					n, err := s.store.GetNode(h.NodeID)
					if err != nil {
						continue
					}
					if n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
						// first hop might be relay when only relay+exit — skip for client entry
						continue
					}
					host := n.Hostname
					if host == "" {
						host = n.PublicIP
					}
					if host == "" {
						continue
					}
					name := n.Name
					if name == "" {
						name = host
					}
					port := n.EffectiveListenPort()
					key := fmt.Sprintf("%s:%d", host, port)
					if seen[key] {
						continue
					}
					seen[key] = true
					proxies = append(proxies, entryProxy{Name: name, Host: host, Port: port})
					break
				}
			}
		}

		// Fallback: all entry/hybrid nodes (legacy / unbound route).
		if len(proxies) == 0 {
			nodes, _ := s.store.ListNodes()
			for _, n := range nodes {
				if n.Role != model.RoleEntry && n.Role != model.RoleHybrid {
					continue
				}
				host := n.Hostname
				if host == "" {
					host = n.PublicIP
				}
				if host == "" {
					continue
				}
				name := n.Name
				if name == "" {
					name = host
				}
				port := n.EffectiveListenPort()
				key := fmt.Sprintf("%s:%d", host, port)
				if seen[key] {
					continue
				}
				seen[key] = true
				proxies = append(proxies, entryProxy{Name: name, Host: host, Port: port})
			}
		}

		var b strings.Builder
		b.WriteString("# mieru-panel subscription\n")
		b.WriteString("# user: " + u.Username + "\n")
		b.WriteString("proxies:\n")
		names := []string{}
		if len(proxies) == 0 {
			b.WriteString("  - name: \"placeholder-no-entry\"\n    type: socks5\n    server: 127.0.0.1\n    port: 1\n")
			names = append(names, "placeholder-no-entry")
		} else {
			for _, p := range proxies {
				fmt.Fprintf(&b, "  - name: %q\n    type: socks5\n    server: %s\n    port: %d\n    username: %q\n    password: %q\n",
					p.Name, p.Host, p.Port, u.Username, u.ProxyPassword)
				names = append(names, p.Name)
			}
		}
		b.WriteString("proxy-groups:\n  - name: PROXY\n    type: select\n    proxies:\n")
		for _, name := range names {
			fmt.Fprintf(&b, "      - %q\n", name)
		}
		b.WriteString("rules:\n  - MATCH,PROXY\n")
		c.Header("Content-Disposition", "attachment; filename=subscription.yaml")
		c.Data(http.StatusOK, "text/yaml; charset=utf-8", []byte(b.String()))
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
	if req.Message == "degraded" {
		status = model.StatusDegraded
	}
	_ = s.store.Heartbeat(req.NodeID, req.PublicIP, req.Hostname, status)
	n, _ := s.store.GetNode(req.NodeID)
	needPull := n != nil && n.ConfigVersion > req.ConfigVersion
	c.JSON(http.StatusOK, gin.H{"ok": true, "config_version": n.ConfigVersion, "need_pull": needPull})
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
