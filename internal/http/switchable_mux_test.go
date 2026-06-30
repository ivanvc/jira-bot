package http

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSwitchableMux_ServeHTTP_DelegatesToInitialMux verifies that ServeHTTP
// routes requests through the mux provided at construction time.
// Validates: Requirements 3.3, 3.4
func TestSwitchableMux_ServeHTTP_DelegatesToInitialMux(t *testing.T) {
	initial := http.NewServeMux()
	initial.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("initial"))
	})

	sm := NewSwitchableMux(initial)

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	rec := httptest.NewRecorder()

	sm.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "initial", rec.Body.String())
}

// TestSwitchableMux_Swap_DelegatesToNewMux verifies that after calling Swap,
// ServeHTTP routes requests through the new mux and the old routes are gone.
// Validates: Requirements 3.3, 3.4
func TestSwitchableMux_Swap_DelegatesToNewMux(t *testing.T) {
	initial := http.NewServeMux()
	initial.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("setup"))
	})

	replacement := http.NewServeMux()
	replacement.HandleFunc("/operational", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("operational"))
	})

	sm := NewSwitchableMux(initial)
	sm.Swap(replacement)

	// New route should be reachable.
	req := httptest.NewRequest(http.MethodGet, "/operational", nil)
	rec := httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "operational", rec.Body.String())

	// Old route should return 404.
	req = httptest.NewRequest(http.MethodGet, "/setup", nil)
	rec = httptest.NewRecorder()
	sm.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestSwitchableMux_ConcurrentAccess_NoPanic verifies that concurrent reads
// (ServeHTTP) and writes (Swap) do not cause a data race or panic.
// Run with -race to confirm no races are detected.
// Validates: Requirements 3.4
func TestSwitchableMux_ConcurrentAccess_NoPanic(t *testing.T) {
	muxA := http.NewServeMux()
	muxA.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("A"))
	})

	muxB := http.NewServeMux()
	muxB.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("B"))
	})

	sm := NewSwitchableMux(muxA)

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half the goroutines perform swaps.
	for i := 0; i < goroutines; i++ {
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				sm.Swap(muxB)
			} else {
				sm.Swap(muxA)
			}
		}(i)
	}

	// Half the goroutines perform requests.
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/ping", nil)
			rec := httptest.NewRecorder()
			sm.ServeHTTP(rec, req)
			// Response must be from one of the two muxes.
			assert.Contains(t, []string{"A", "B"}, rec.Body.String())
		}()
	}

	wg.Wait()
}
