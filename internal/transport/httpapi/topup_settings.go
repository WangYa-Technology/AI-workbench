package httpapi

import (
	"net/http"

	"github.com/hcai-chat/hcai-chat/internal/admin"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func (s *Server) walletTopupSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	s.writeWalletTopupSettings(w, r)
}

func (s *Server) adminWalletTopupSettings(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requirePermission(w, r, "admin:finance"); !ok {
		return
	}
	s.writeWalletTopupSettings(w, r)
}

func (s *Server) writeWalletTopupSettings(w http.ResponseWriter, r *http.Request) {
	item, err := s.billing.WalletTopupSettings(r.Context())
	if err != nil {
		s.internalError(w, r, "read wallet top-up settings", err)
		return
	}
	httputil.JSON(w, http.StatusOK, item)
}

func (s *Server) adminUpdateWalletTopupSettings(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.requirePermission(w, r, "admin:finance")
	if !ok {
		return
	}
	var input admin.WalletTopupSettingsUpdate
	if !httputil.DecodeJSON(w, r, &input) {
		return
	}
	item, err := s.admin.UpdateWalletTopupSettings(r.Context(), actor.ID, input, httputil.RequestID(r.Context()))
	s.writeAdminResult(w, r, item, err)
}
