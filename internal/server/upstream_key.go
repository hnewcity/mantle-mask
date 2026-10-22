package server

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
)

func (s *Server) updateUpstreamAPIKey(c *gin.Context) {
	var input struct {
		APIKey string `json:"api_key"`
	}
	if !decode(c, &input) {
		return
	}
	id := c.Param("id")
	if err := s.store.UpdateUpstreamAPIKey(id, input.APIKey); err != nil {
		switch {
		case errors.Is(err, config.ErrUpstreamNotFound):
			abortError(c, http.StatusNotFound, "upstream_not_found", err.Error())
		case errors.Is(err, config.ErrUpstreamAuthMode):
			abortError(c, http.StatusBadRequest, "invalid_auth_mode", err.Error())
		case errors.Is(err, config.ErrInvalidAPIKey):
			abortError(c, http.StatusBadRequest, "invalid_api_key", err.Error())
		default:
			abortError(c, http.StatusServiceUnavailable, "credential_update_failed", "Could not save upstream API key.")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "updated": true})
}
