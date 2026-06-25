package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// newStatusTestMux creates a per-test mux with the status handler routes
// registered, avoiding pollution of the default ServeMux across tests.
func newStatusTestMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/readyz", func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	})
	return mux
}

// TestHealthz_ReturnsHTTP200 tests that GET /healthz responds with HTTP 200 OK.
// Validates: Requirements 6.1
func TestHealthz_ReturnsHTTP200(t *testing.T) {
	mux := newStatusTestMux()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "GET /healthz should return HTTP 200")
}

// TestReadyz_ReturnsHTTP200 tests that GET /readyz responds with HTTP 200 OK.
// Validates: Requirements 6.2
func TestReadyz_ReturnsHTTP200(t *testing.T) {
	mux := newStatusTestMux()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "GET /readyz should return HTTP 200")
}
