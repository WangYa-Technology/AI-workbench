package httpapi

import (
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) siteConfiguration(w http.ResponseWriter, r *http.Request) {
	configuration, err := s.admin.GetSiteConfiguration(r.Context())
	if err != nil {
		s.internalError(w, r, "get public site configuration", err)
		return
	}
	httputil.JSON(w, http.StatusOK, configuration)
}
