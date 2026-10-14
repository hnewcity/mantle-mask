package server

import (
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
	"github.com/hnewcity/mantle-mask/internal/gateway"
)

const apiKeyContext = "mantle.apiKey"
const gatewayStateContext = "mantle.gatewayState"

func bearer(r *http.Request) string {
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}

func abortError(c *gin.Context, status int, code, message string) {
	c.Abort()
	gateway.Error(c.Writer, status, code, message)
}

type responseWriter struct {
	gin.ResponseWriter
	transport http.ResponseWriter
}

func (w *responseWriter) Unwrap() http.ResponseWriter {
	return w.transport
}

func (w *responseWriter) FlushError() error {
	w.WriteHeaderNow()
	return http.NewResponseController(w.transport).Flush()
}

func (w *responseWriter) Flush() {
	_ = w.FlushError()
}

func preserveResponseController(c *gin.Context) {
	// Gin tracks status and bytes, but its Flush method drops transport errors.
	// Expose FlushError and Unwrap so the gateway can still detect disconnects
	// and set per-write deadlines through http.ResponseController.
	transport := c.Writer.(interface{ Unwrap() http.ResponseWriter }).Unwrap()
	c.Writer = &responseWriter{ResponseWriter: c.Writer, transport: transport}
}

func recoverRequest(c *gin.Context) {
	defer func() {
		if recovered := recover(); recovered != nil {
			if recovered == http.ErrAbortHandler {
				panic(recovered)
			}
			// Do not log panic values, request bodies, headers, or query strings:
			// they can contain credentials or inference content.
			slog.Error("request handler panicked", "request_id", c.Writer.Header().Get("X-Request-Id"), "method", c.Request.Method, "route", c.FullPath())
			if c.Writer.Written() {
				// A started stream cannot be replaced by a successful JSON response.
				panic(http.ErrAbortHandler)
			}
			abortError(c, http.StatusInternalServerError, "internal_error", "An internal server error occurred.")
		}
	}()
	c.Next()
}

func (s *Server) headers(c *gin.Context) {
	c.Header("X-Request-Id", config.Token("req_")[:28])
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Frame-Options", "DENY")
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		c.Header("Cache-Control", "no-store")
	}
	origin := c.GetHeader("Origin")
	if origin != "" && s.cors[origin] {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, OpenAI-Project, OpenAI-Beta")
		c.Header("Access-Control-Expose-Headers", "X-Request-Id, X-Mantle-Partial")
	}
	if c.Request.Method == http.MethodOptions {
		if origin != "" && !s.cors[origin] {
			abortError(c, http.StatusForbidden, "origin_not_allowed", "This origin is not allowed.")
			return
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}

func (s *Server) isAdmin(token string) bool {
	hash := config.Hash(token)
	return subtle.ConstantTimeCompare([]byte(hash), []byte(s.adminHash)) == 1
}

func (s *Server) requireAdmin(c *gin.Context) {
	if !s.isAdmin(bearer(c.Request)) {
		abortError(c, http.StatusUnauthorized, "invalid_admin_token", "A valid admin token is required.")
	}
}

func (s *Server) authenticateKey(c *gin.Context, message string) {
	key, ok := s.store.Authenticate(bearer(c.Request))
	if !ok {
		abortError(c, http.StatusUnauthorized, "invalid_api_key", message)
		return
	}
	c.Set(apiKeyContext, key)
}

func (s *Server) requireUser(c *gin.Context) {
	// Keep user-scoped reads on the same configuration generation as authentication.
	s.store.Coordinator().RLock()
	defer s.store.Coordinator().RUnlock()
	s.authenticateKey(c, "A valid user API key is required.")
	if !c.IsAborted() {
		c.Next()
	}
}

func (s *Server) requireGatewayKey(c *gin.Context) {
	key, st, ok := s.store.AuthenticateSnapshot(bearer(c.Request))
	if !ok {
		abortError(c, http.StatusUnauthorized, "invalid_api_key", "A valid gateway API key is required.")
		return
	}
	c.Set(apiKeyContext, key)
	c.Set(gatewayStateContext, st)
}

func currentKey(c *gin.Context) config.APIKey {
	return c.MustGet(apiKeyContext).(config.APIKey)
}
