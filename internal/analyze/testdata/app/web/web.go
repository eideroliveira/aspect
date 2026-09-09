package web

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/app/store"
)

// Handler is the HTTP surface.
type Handler interface {
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

// New builds the router.
func New(s *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Get("/products/{sku}", func(w http.ResponseWriter, req *http.Request) {})
	r.Post("/reservations", reserve(s))
	r.Route("/admin", func(r chi.Router) {})
	http.HandleFunc("/healthz", nil)
	return r
}

func reserve(s *store.Store) http.HandlerFunc { return nil }
