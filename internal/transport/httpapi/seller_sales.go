package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/hcai-chat/hcai-chat/internal/marketplace"
	"github.com/hcai-chat/hcai-chat/internal/platform/httputil"
)

func salesQuery(r *http.Request, list bool) (marketplace.SellerSalesFilter, error) {
	f := marketplace.SellerSalesFilter{}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return f, marketplace.ErrInvalidSaleFilter
		}
		switch key {
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > 50 {
				return f, marketplace.ErrInvalidSaleFilter
			}
			f.Limit = n
		case "cursor":
			f.Cursor = values[0]
		case "status":
			if !list {
				return f, marketplace.ErrInvalidSaleFilter
			}
			f.Status = values[0]
		case "environment":
			if !list {
				return f, marketplace.ErrInvalidSaleFilter
			}
			f.Environment = values[0]
		case "productId":
			if !list {
				return f, marketplace.ErrInvalidSaleFilter
			}
			id, err := uuid.Parse(values[0])
			if err != nil || id == uuid.Nil {
				return f, marketplace.ErrInvalidSaleFilter
			}
			f.ProductID = id
		default:
			return f, marketplace.ErrInvalidSaleFilter
		}
	}
	return f, nil
}

func (s *Server) listSellerSales(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	f, err := salesQuery(r, true)
	if err != nil {
		s.writeSaleResult(w, r, nil, err)
		return
	}
	page, err := s.marketplace.ListSales(r.Context(), actor, f)
	s.writeSaleResult(w, r, page, err)
}

func (s *Server) getSellerSale(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		s.writeSaleResult(w, r, nil, marketplace.ErrInvalidSaleFilter)
		return
	}
	item, err := s.marketplace.GetSale(r.Context(), actor, id)
	s.writeSaleResult(w, r, item, err)
}

func (s *Server) listSellerSaleEvents(w http.ResponseWriter, r *http.Request) {
	actor, ok := s.listingUser(w, r, false)
	if !ok {
		return
	}
	id, ok := pathUUID(w, r, "orderID")
	if !ok {
		return
	}
	f, err := salesQuery(r, false)
	if err != nil {
		s.writeSaleResult(w, r, nil, err)
		return
	}
	page, err := s.marketplace.SaleEvents(r.Context(), actor, id, f.Cursor, f.Limit)
	s.writeSaleResult(w, r, page, err)
}

func (s *Server) writeSaleResult(w http.ResponseWriter, r *http.Request, item any, err error) {
	switch {
	case errors.Is(err, marketplace.ErrInvalidSaleFilter):
		httputil.WriteError(w, r, 422, "invalid_seller_sales_filter", "Check sales filters and pagination.", false)
	case errors.Is(err, marketplace.ErrNotFound):
		httputil.WriteError(w, r, 404, "sale_not_found", "The sale was not found.", false)
	case errors.Is(err, marketplace.ErrListingForbidden):
		httputil.WriteError(w, r, 403, "forbidden", "Sales are available only to the active seller.", false)
	case err != nil:
		s.internalError(w, r, "seller sales", err)
	default:
		httputil.JSON(w, http.StatusOK, item)
	}
}
