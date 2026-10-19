package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/usage"
)

func (s *Server) session(c *gin.Context) {
	s.store.Coordinator().RLock()
	defer s.store.Coordinator().RUnlock()
	token := bearer(c.Request)
	if s.isAdmin(token) {
		c.JSON(http.StatusOK, gin.H{"role": "admin", "version": "0.2.0"})
		return
	}
	key, ok := s.store.Authenticate(token)
	if !ok {
		abortError(c, http.StatusUnauthorized, "invalid_token", "A valid admin token or user API key is required.")
		return
	}
	user, _ := s.store.User(key.UserID)
	c.JSON(http.StatusOK, gin.H{"role": "user", "user": user, "key": key})
}

func (s *Server) profile(c *gin.Context) {
	key := currentKey(c)
	user, _ := s.store.User(key.UserID)
	c.JSON(http.StatusOK, gin.H{"user": user, "key": key})
}

func (s *Server) userKeys(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.store.Keys(currentKey(c).UserID, true)})
}

func (s *Server) report(c *gin.Context, userID string) {
	f, err := usage.ParseFilter(c.Request.URL.Query(), time.Now())
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_usage_filter", err.Error())
		return
	}
	if userID != "" {
		// Authentication, never query parameters, determines a user's scope.
		f.UserID = userID
		// Upstream configuration is administrator-only, including attribution filters.
		f.UpstreamID = ""
	}
	report, err := s.gateway.Usage(c.Request.Context(), f)
	if err != nil {
		abortError(c, http.StatusServiceUnavailable, "usage_storage_unavailable", "Usage statistics are temporarily unavailable.")
		return
	}
	for i := range report.ByUser {
		if u, ok := s.store.User(report.ByUser[i].ID); ok {
			report.ByUser[i].Name = u.Name
		}
	}
	if userID != "" {
		report.ByUpstream = nil
	} else {
		names := make(map[string]string)
		for _, u := range s.store.RoutingSnapshot().Upstreams {
			names[u.ID] = u.Name
		}
		for i := range report.ByUpstream {
			if name, ok := names[report.ByUpstream[i].ID]; ok {
				report.ByUpstream[i].Name = name
			}
		}
	}
	c.JSON(http.StatusOK, report)
}
