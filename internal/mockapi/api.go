// Package mockapi is a mock shop REST API that the mock MCP server wraps.
//
// It behaves like a small v1 API until chaos switches are turned on. The
// OpenAPI spec at /openapi.json changes with the switches, the same way a
// real vendor's published spec would.
package mockapi

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"
)

type API struct {
	Chaos *Chaos

	mu       sync.Mutex
	products map[string]map[string]any
	orders   map[string]map[string]any
}

func New() *API {
	return &API{
		Chaos: &Chaos{},
		products: map[string]map[string]any{
			"p1": {"id": "p1", "name": "Trekking Jacket", "price": 1499.0, "currency": "SEK", "stock": 12},
			"p2": {"id": "p2", "name": "Hiking Pants", "price": 899.0, "currency": "SEK", "stock": 30},
			"p3": {"id": "p3", "name": "Wool Beanie", "price": 249.0, "currency": "SEK", "stock": 0},
		},
		orders: map[string]map[string]any{},
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /openapi.json", a.openapi)
	mux.HandleFunc("GET /_chaos", a.getChaos)
	mux.HandleFunc("PUT /_chaos", a.putChaos)
	mux.HandleFunc("GET /v1/products", a.listProducts)
	mux.HandleFunc("GET /v1/products/{id}", a.getProduct)
	mux.HandleFunc("POST /v1/orders", a.createOrder)
	mux.HandleFunc("GET /v1/orders/{id}", a.getOrder)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		a.writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
	})
	return a.chaosMiddleware(mux)
}

// chaosMiddleware applies transport-level chaos: latency, 5xx, path version and auth.
func (a *API) chaosMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, open := range []string{"/health", "/openapi.json", "/_chaos"} {
			if r.URL.Path == open {
				next.ServeHTTP(w, r)
				return
			}
		}
		if a.Chaos.On("slow") {
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		if a.Chaos.On("flaky_5xx") {
			a.writeError(w, http.StatusServiceUnavailable, "service unavailable")
			return
		}
		current := "/" + a.Chaos.Version() + "/"
		if !strings.HasPrefix(r.URL.Path, current) {
			a.writeError(w, http.StatusNotFound, "not found: "+r.URL.Path)
			return
		}
		// Handlers are registered under /v1; route /v2 requests to them.
		r.URL.Path = "/v1/" + strings.TrimPrefix(r.URL.Path, current)
		r.URL.RawPath = ""
		if !a.authorized(r) {
			a.writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *API) authorized(r *http.Request) bool {
	key := a.Chaos.ValidKey()
	if a.Chaos.On("auth_bearer") {
		return r.Header.Get("Authorization") == "Bearer "+key
	}
	return r.Header.Get("X-API-Key") == key
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func (a *API) writeError(w http.ResponseWriter, status int, message string) {
	if a.Chaos.On("error_format") {
		if status == http.StatusNotFound {
			status = http.StatusGone
		}
		writeJSON(w, status, map[string]any{
			"errors": []map[string]string{{"code": fmt.Sprintf("E%d", status), "message": message}},
		})
		return
	}
	writeJSON(w, status, map[string]string{"detail": message})
}

func (a *API) serializeProduct(p map[string]any) map[string]any {
	out := maps.Clone(p)
	if a.Chaos.On("rename_field") {
		out["unit_price"] = out["price"]
		delete(out, "price")
	}
	if a.Chaos.On("remove_field") {
		delete(out, "stock")
	}
	return out
}

func (a *API) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": a.Chaos.Version()})
}

func (a *API) listProducts(w http.ResponseWriter, _ *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := []map[string]any{}
	for _, id := range []string{"p1", "p2", "p3"} {
		out = append(out, a.serializeProduct(a.products[id]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) getProduct(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := r.PathValue("id")
	p, ok := a.products[id]
	if !ok {
		a.writeError(w, http.StatusNotFound, fmt.Sprintf("product %s not found", id))
		return
	}
	writeJSON(w, http.StatusOK, a.serializeProduct(p))
}

func (a *API) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProductID string `json:"product_id"`
		Quantity  int    `json:"quantity"`
		Currency  string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ProductID == "" || body.Quantity < 1 {
		a.writeError(w, http.StatusUnprocessableEntity, "product_id and quantity >= 1 are required")
		return
	}
	if a.Chaos.On("required_param") && body.Currency == "" {
		a.writeError(w, http.StatusUnprocessableEntity, "currency is required")
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p, ok := a.products[body.ProductID]
	if !ok {
		a.writeError(w, http.StatusNotFound, fmt.Sprintf("product %s not found", body.ProductID))
		return
	}
	currency := body.Currency
	if currency == "" {
		currency = p["currency"].(string)
	}
	id := fmt.Sprintf("o%d", len(a.orders)+1)
	a.orders[id] = map[string]any{
		"id":         id,
		"product_id": body.ProductID,
		"quantity":   body.Quantity,
		"total":      p["price"].(float64) * float64(body.Quantity),
		"currency":   currency,
		"status":     "created",
	}
	writeJSON(w, http.StatusCreated, a.orders[id])
}

func (a *API) getOrder(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	id := r.PathValue("id")
	o, ok := a.orders[id]
	if !ok {
		a.writeError(w, http.StatusNotFound, fmt.Sprintf("order %s not found", id))
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (a *API) getChaos(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"active": a.Chaos.Active(), "available": Switches})
}

func (a *API) putChaos(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Active []string `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := a.Chaos.Set(body.Active...); err != nil {
		a.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"active": a.Chaos.Active()})
}
