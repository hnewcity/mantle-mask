package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
	"github.com/hnewcity/mantle-mask/internal/pricing"
	webassets "github.com/hnewcity/mantle-mask/web"
)

type Server struct {
	pricingSyncMu sync.Mutex
	syncPrices    func(context.Context, *config.PriceCatalog, []string) (config.PriceCatalog, error)
	backupMu      sync.Mutex
	store         *config.Store
	gateway       *gateway.Gateway
	adminHash     string
	cors          map[string]bool
	files         http.Handler
	assets        fs.FS
	router        *gin.Engine
}

func New(store *config.Store, proxy *gateway.Gateway, adminToken, origins string) *Server {
	assets, _ := fs.Sub(webassets.Assets, "dist")
	s := &Server{syncPrices: pricing.New().Sync, store: store, gateway: proxy, adminHash: config.Hash(adminToken), cors: map[string]bool{}, files: http.FileServer(http.FS(assets)), assets: assets}
	for _, origin := range strings.Split(origins, ",") {
		if origin = strings.TrimSpace(origin); origin != "" && origin != "*" {
			s.cors[origin] = true
		}
	}
	s.router = gin.New()
	// Preserve exact API paths: redirects must not rewrite authenticated requests.
	s.router.RedirectTrailingSlash = false
	s.router.RedirectFixedPath = false
	_ = s.router.SetTrustedProxies(nil)
	s.router.Use(recoverRequest, preserveResponseController, s.headers)
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

func (s *Server) routes() {
	s.router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "mantle-mask"})
	})

	api := s.router.Group("/api")
	api.GET("/session", s.session)

	admin := api.Group("/admin", s.requireAdmin, s.guardPendingRestore)
	admin.GET("/session", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "authenticated", "role": "admin", "version": "0.4.0"})
	})
	admin.GET("/users", s.listUsers)
	admin.POST("/users", s.saveUser)
	admin.PUT("/users/:id", s.saveUser)
	admin.GET("/usage", func(c *gin.Context) { s.report(c, "") })
	admin.GET("/usage/pricing", func(c *gin.Context) {
		prices := s.store.PricingSnapshot().ModelPrices
		if prices == nil {
			prices = map[string]config.ModelPrice{}
		}
		c.JSON(http.StatusOK, prices)
	})
	admin.PUT("/usage/pricing", s.saveModelPrices)
	admin.GET("/usage/pricing/reference", func(c *gin.Context) {
		catalog := s.store.PricingSnapshot().PriceCatalog
		if catalog == nil {
			catalog = &config.PriceCatalog{Entries: []config.ReferencePrice{}, Sources: []config.PriceSourceStatus{}, Warnings: []string{}}
		}
		c.JSON(http.StatusOK, catalog)
	})
	admin.POST("/usage/pricing/sync", s.synchronizeModelPrices)
	admin.GET("/overview", func(c *gin.Context) { c.JSON(http.StatusOK, s.gateway.Metrics()) })
	admin.GET("/logs", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": s.gateway.Logs()}) })
	admin.GET("/upstreams", s.listUpstreams)
	admin.POST("/upstreams", s.saveUpstream)
	admin.PUT("/upstreams/:id", s.saveUpstream)
	admin.PUT("/upstreams/:id/api-key", s.updateUpstreamAPIKey)
	admin.DELETE("/upstreams/:id", s.deleteUpstream)
	admin.POST("/upstreams/:id/test", s.testUpstream)
	admin.GET("/keys", s.listKeys)
	admin.POST("/keys", s.createKey)
	admin.DELETE("/keys/:id", s.deleteKey)
	admin.GET("/settings", func(c *gin.Context) { c.JSON(http.StatusOK, s.store.Settings()) })
	admin.PUT("/settings", s.saveSettings)
	admin.POST("/backup/export", s.exportBackup)
	admin.POST("/backup/restore", s.restoreBackup)
	admin.GET("/playground/*path", s.playground)
	admin.POST("/playground/*path", s.playground)

	self := api.Group("/me", s.requireUser)
	self.GET("", s.profile)
	self.GET("/keys", s.userKeys)
	self.GET("/usage", func(c *gin.Context) { s.report(c, currentKey(c).UserID) })

	v1 := s.router.Group("/v1", s.requireGatewayKey)
	v1.GET("/models", s.proxy)
	v1.POST("/chat/completions", s.proxy)
	v1.POST("/responses", s.proxy)
	v1.GET("/responses/:id", s.proxy)
	v1.DELETE("/responses/:id", s.proxy)
	v1.POST("/responses/:id/cancel", s.proxy)
	v1.GET("/responses/:id/input_items", s.proxy)

	s.router.NoRoute(s.fallback)
}

func (s *Server) proxy(c *gin.Context) {
	key := currentKey(c)
	st := c.MustGet(gatewayStateContext).(config.State)
	var user config.User
	for _, u := range st.Users {
		if u.ID == key.UserID {
			user = u
			break
		}
	}
	s.gateway.ProxyWithState(c.Writer, c.Request, gateway.Principal{ID: key.ID, Name: key.Name, UserID: user.ID, UserName: user.Name}, st)
}

func (s *Server) playground(c *gin.Context) {
	clone := c.Request.Clone(c.Request.Context())
	clone.URL.Path = "/v1" + c.Param("path")
	s.gateway.Proxy(c.Writer, clone, gateway.Principal{ID: "admin-playground", Name: "Playground", UserID: "system-admin", UserName: "管理员"})
}

// Gin's NoRoute chain does not inherit group middleware. Authenticate unknown
// API routes too, so they cannot fall through to the public SPA or bypass auth.
func (s *Server) fallback(c *gin.Context) {
	path := c.Request.URL.Path
	switch {
	case path == "/api/me" || strings.HasPrefix(path, "/api/me/"):
		s.requireUser(c)
		if c.IsAborted() {
			return
		}
		if c.Request.Method != http.MethodGet {
			abortError(c, http.StatusMethodNotAllowed, "method_not_allowed", "This endpoint only supports GET.")
			return
		}
		abortError(c, http.StatusNotFound, "not_found", "Unknown user endpoint.")
	case strings.HasPrefix(path, "/api/"):
		s.requireAdmin(c)
		if !c.IsAborted() {
			abortError(c, http.StatusNotFound, "not_found", "Unknown admin endpoint.")
		}
	case path == "/v1" || strings.HasPrefix(path, "/v1/"):
		s.requireGatewayKey(c)
		if !c.IsAborted() {
			s.proxy(c)
		}
	default:
		s.frontend(c)
	}
}

func (s *Server) frontend(c *gin.Context) {
	w, r := c.Writer, c.Request
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		abortError(c, http.StatusNotFound, "not_found", "Not found.")
		return
	}
	// NoRoute starts with status 404; SPA and static files need a success default.
	c.Status(http.StatusOK)
	c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'")
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name != "" {
		if stat, err := fs.Stat(s.assets, name); err == nil && !stat.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				c.Header("Cache-Control", "public, max-age=31536000, immutable")
			}
			s.files.ServeHTTP(w, r)
			return
		}
		if strings.Contains(name, ".") {
			http.NotFound(w, r)
			return
		}
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache")
	data, err := fs.ReadFile(s.assets, "index.html")
	if err != nil {
		data = webassets.Fallback
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func decode(c *gin.Context, dst any) bool {
	reader := http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_body", "Invalid JSON or unknown configuration field.")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		abortError(c, http.StatusBadRequest, "invalid_body", "Expected a single JSON object.")
		return false
	}
	return true
}
