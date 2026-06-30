package http

import (
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/common"
)

type Server struct {
	*http.Server
	*common.State
	Mux *SwitchableMux
}

// NewServer returns a new Server. It selects the appropriate mux based on the
// current AuthMode and wraps it in a SwitchableMux so that routes can be
// atomically swapped later (e.g., after an OAuth setup-to-oauth2 transition).
// The coordinator parameter is optional (nil for non-setup modes); when provided
// in setup mode, it is forwarded to BuildSetupMux so the setup handler can
// trigger a live transition after successful token persistence.
func NewServer(state *common.State, coordinator TransitionCoordinatorInterface) *Server {
	stdlog := log.Default().StandardLog(log.StandardLogOptions{
		ForceLevel: log.ErrorLevel,
	})

	var initialMux *http.ServeMux
	if state.Config.AuthMode == "oauth2-setup" {
		initialMux = BuildSetupMux(state, coordinator)
	} else {
		initialMux = BuildOAuth2Mux(state)
	}

	switchable := NewSwitchableMux(initialMux)

	return &Server{
		Server: &http.Server{
			Addr:     state.Config.ListenHTTP,
			Handler:  switchable,
			ErrorLog: stdlog,
		},
		State: state,
		Mux:   switchable,
	}
}

// Start starts the HTTP server.
func (s *Server) Start() error {
	log.Info("Starting HTTP server", "listen", s.Addr)

	if err := s.ListenAndServe(); err != nil {
		log.Error("Error starting Web Server", "error", err)
		return err
	}

	return nil
}
