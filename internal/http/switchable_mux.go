package http

import (
	"net/http"
	"sync/atomic"
)

// SwitchableMux implements http.Handler and delegates to an atomically-swappable ServeMux.
// This enables atomic route transitions without affecting in-flight requests or requiring
// a listener restart.
type SwitchableMux struct {
	current atomic.Pointer[http.ServeMux]
}

// NewSwitchableMux creates a SwitchableMux initialized with the given mux.
func NewSwitchableMux(initial *http.ServeMux) *SwitchableMux {
	s := &SwitchableMux{}
	s.current.Store(initial)
	return s
}

// ServeHTTP delegates the request to the current mux loaded atomically.
func (s *SwitchableMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.current.Load().ServeHTTP(w, r)
}

// Swap atomically replaces the current mux with newMux.
func (s *SwitchableMux) Swap(newMux *http.ServeMux) {
	s.current.Store(newMux)
}
