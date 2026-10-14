package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hnewcity/mantle-mask/internal/config"
)

func (s *Server) listUsers(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.store.Snapshot().Users})
}

func (s *Server) saveUser(c *gin.Context) {
	input := config.UserInput{Enabled: true}
	if !decode(c, &input) {
		return
	}
	id := c.Param("id")
	user, err := s.store.SaveUser(id, input)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_user", err.Error())
		return
	}
	status := http.StatusOK
	if id == "" {
		status = http.StatusCreated
	}
	c.JSON(status, user)
}

func (s *Server) listUpstreams(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": s.store.Views()})
}

func (s *Server) saveUpstream(c *gin.Context) {
	var input config.UpstreamInput
	if !decode(c, &input) {
		return
	}
	saved, err := s.store.SaveUpstream(c.Param("id"), input)
	if err != nil {
		abortError(c, http.StatusBadRequest, "invalid_upstream", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": saved})
}

func (s *Server) deleteUpstream(c *gin.Context) {
	if err := s.store.DeleteUpstream(c.Param("id")); err != nil {
		abortError(c, http.StatusBadRequest, "delete_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) testUpstream(c *gin.Context) {
	models, err := s.gateway.TestUpstream(c.Request.Context(), c.Param("id"))
	if err != nil {
		abortError(c, http.StatusBadGateway, "connection_test_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok", "models": models})
}

func (s *Server) listKeys(c *gin.Context) {
	keys := s.store.Keys(c.Query("user_id"), c.Query("include_revoked") == "true")
	c.JSON(http.StatusOK, gin.H{"data": keys})
}

func (s *Server) createKey(c *gin.Context) {
	var input struct {
		Name   string `json:"name"`
		UserID string `json:"user_id"`
	}
	if !decode(c, &input) {
		return
	}
	key, token, err := s.store.CreateKey(input.Name, input.UserID)
	if err != nil {
		abortError(c, http.StatusBadRequest, "key_create_failed", err.Error())
		return
	}
	c.JSON(http.StatusCreated, gin.H{"key": key, "token": token})
}

func (s *Server) deleteKey(c *gin.Context) {
	if err := s.store.DeleteKey(c.Param("id")); err != nil {
		abortError(c, http.StatusBadRequest, "key_delete_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func (s *Server) saveSettings(c *gin.Context) {
	var input config.Settings
	if !decode(c, &input) {
		return
	}
	if err := s.store.UpdateSettings(input); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_settings", err.Error())
		return
	}
	c.JSON(http.StatusOK, input)
}

func (s *Server) saveModelPrices(c *gin.Context) {
	var input map[string]config.ModelPrice
	if !decode(c, &input) {
		return
	}
	if input == nil {
		abortError(c, http.StatusBadRequest, "invalid_model_prices", "Model prices must be an object.")
		return
	}
	if err := s.store.UpdateModelPrices(input); err != nil {
		abortError(c, http.StatusBadRequest, "invalid_model_prices", err.Error())
		return
	}
	c.JSON(http.StatusOK, input)
}

func (s *Server) synchronizeModelPrices(c *gin.Context) {
	if !s.pricingSyncMu.TryLock() {
		abortError(c, http.StatusConflict, "price_sync_in_progress", "Price synchronization is already running.")
		return
	}
	defer s.pricingSyncMu.Unlock()
	state := s.store.Snapshot()
	regions := []string{}
	for _, u := range state.Upstreams {
		if region := config.PricingRegion(u); region != "" {
			regions = append(regions, region)
		}
	}
	catalog, err := s.syncPrices(c.Request.Context(), state.PriceCatalog, regions)
	if err != nil {
		abortError(c, http.StatusBadGateway, "price_sync_failed", err.Error())
		return
	}
	if err := s.store.UpdatePriceCatalog(catalog); err != nil {
		abortError(c, http.StatusServiceUnavailable, "price_storage_failed", err.Error())
		return
	}
	c.JSON(http.StatusOK, catalog)
}
