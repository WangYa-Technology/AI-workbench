package httpapi

import (
	"context"
	"errors"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
	"github.com/hcai-chat/hcai-chat/internal/platform/media"
	"github.com/hcai-chat/hcai-chat/internal/productdelivery"
)

func (s *Server) deliveryRepairUser(w http.ResponseWriter, r *http.Request) (uuid.UUID, uuid.UUID, bool) {
	w.Header().Set("Cache-Control", "private, no-store")
	actor, ok := s.requirePermission(w, r, "admin:media")
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	id, ok := pathUUID(w, r, "orderID")
	return actor.ID, id, ok
}
func (s *Server) inspectProductDelivery(w http.ResponseWriter, r *http.Request) {
	actor, id, ok := s.deliveryRepairUser(w, r)
	if !ok {
		return
	}
	item, err := s.deliveryRepairs.Inspect(r.Context(), actor, id)
	s.writeDeliveryRepair(w, r, item, err)
}
func (s *Server) repairProductDelivery(w http.ResponseWriter, r *http.Request) {
	actor, id, ok := s.deliveryRepairUser(w, r)
	if !ok {
		return
	}
	var in productdelivery.RepairInput
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	item, err := s.deliveryRepairs.Repair(r.Context(), actor, id, idempotencyKey(r), httputil.RequestID(r.Context()), in)
	s.writeDeliveryRepair(w, r, item, err)
}

func (s *Server) prepareProductDeliveryUpload(w http.ResponseWriter, r *http.Request) {
	actor, id, ok := s.deliveryRepairUser(w, r)
	if !ok {
		return
	}
	var in productdelivery.RepairInput
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	item, err := s.deliveryRepairs.PrepareUpload(r.Context(), actor, id, idempotencyKey(r), httputil.RequestID(r.Context()), in)
	s.writeDeliveryRepair(w, r, item, err)
}

func (s *Server) uploadProductDeliveryRepair(w http.ResponseWriter, r *http.Request) {
	actor, id, ok := s.deliveryRepairUser(w, r)
	if !ok {
		return
	}
	repair, ok := pathUUID(w, r, "repairID")
	if !ok {
		return
	}
	query := r.URL.Query()
	if len(query) != 1 || len(query["confirmed"]) != 1 || query.Get("confirmed") != "true" {
		s.writeDeliveryRepair(w, r, productdelivery.RepairStatus{}, productdelivery.ErrRepairInvalid)
		return
	}
	typeName, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typeName != "application/octet-stream" || len(params) != 0 {
		httputil.WriteError(w, r, 415, "delivery_repair_upload_type", "Send raw backup bytes as application/octet-stream.", false)
		return
	}
	if r.ContentLength > productdelivery.MaxBytes {
		httputil.WriteError(w, r, 413, "delivery_repair_upload_too_large", "Delivery recovery supports at most 100 MiB.", false)
		return
	}
	// Only this authenticated, bounded endpoint extends the API's normal 15s
	// body deadline. Keep an absolute limit for slow clients and final storage IO.
	controller := http.NewResponseController(w)
	if err = controller.SetReadDeadline(time.Now().Add(5 * time.Minute)); err != nil {
		s.internalError(w, r, "set repair upload deadline", err)
		return
	}
	if err = controller.SetWriteDeadline(time.Now().Add(7 * time.Minute)); err != nil {
		s.internalError(w, r, "set repair response deadline", err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 7*time.Minute)
	defer cancel()
	r.Body = http.MaxBytesReader(w, r.Body, productdelivery.MaxBytes)
	item, err := s.deliveryRepairs.Upload(ctx, actor, id, repair, true, httputil.RequestID(r.Context()), r.Body)
	s.writeDeliveryRepair(w, r, item, err)
}
func (s *Server) resumeProductDeliveryRepair(w http.ResponseWriter, r *http.Request) {
	actor, id, ok := s.deliveryRepairUser(w, r)
	if !ok {
		return
	}
	repairID, ok := pathUUID(w, r, "repairID")
	if !ok {
		return
	}
	var in struct {
		Confirmed bool `json:"confirmed"`
	}
	if !httputil.DecodeJSON(w, r, &in) {
		return
	}
	if !in.Confirmed {
		s.writeDeliveryRepair(w, r, productdelivery.RepairStatus{}, productdelivery.ErrRepairInvalid)
		return
	}
	item, err := s.deliveryRepairs.Resume(r.Context(), actor, id, repairID, httputil.RequestID(r.Context()))
	s.writeDeliveryRepair(w, r, item, err)
}
func (s *Server) writeDeliveryRepair(w http.ResponseWriter, r *http.Request, item productdelivery.RepairStatus, err error) {
	var tooLarge *http.MaxBytesError
	var timeout net.Error
	switch {
	case errors.As(err, &tooLarge):
		httputil.WriteError(w, r, 413, "delivery_repair_upload_too_large", "Delivery recovery supports at most 100 MiB.", false)
	case errors.Is(err, productdelivery.ErrRepairBusy):
		w.Header().Set("Retry-After", "5")
		httputil.WriteError(w, r, 503, "delivery_repair_busy", "Recovery upload capacity is busy. Retry the recorded repair.", true)
	case errors.Is(err, productdelivery.ErrRepairUploadRequired):
		httputil.WriteError(w, r, 409, "delivery_repair_upload_required", "Select the original backup file and continue uploading to this repair.", false)
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeout) && timeout.Timeout()):
		httputil.WriteError(w, r, 408, "delivery_repair_upload_timeout", "The upload timed out. Continue the recorded repair with the same file.", true)
	case errors.Is(err, productdelivery.ErrRepairForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "Media operations permission is required.", false)
	case errors.Is(err, productdelivery.ErrRepairNotFound):
		httputil.WriteError(w, r, 404, "delivery_repair_not_found", "The immutable delivery was not found.", false)
	case errors.Is(err, productdelivery.ErrRepairInvalid):
		httputil.WriteError(w, r, 422, "invalid_delivery_repair", "Confirm the repair and provide a current revision, independent backup and reason.", false)
	case errors.Is(err, productdelivery.ErrRepairConflict):
		httputil.WriteError(w, r, 409, "delivery_repair_conflict", "Refresh the delivery; the revision or retention eligibility changed.", false)
	case errors.Is(err, media.ErrIntegrity):
		httputil.WriteError(w, r, 422, "delivery_repair_integrity", "The bytes do not match the accepted delivery.", false)
	case errors.Is(err, media.ErrNotFound):
		httputil.WriteError(w, r, 409, "delivery_repair_source_missing", "The original is unavailable; select an uploaded verified backup.", false)
	case err != nil:
		s.internalError(w, r, "repair product delivery", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
