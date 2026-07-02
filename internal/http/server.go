package http

import (
	"net/http"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/common"
)

type Server struct {
	*http.Server
	*common.State
}

// NewServer returns a new Server with a standard ServeMux configured for
// per-user token mode.
func NewServer(state *common.State) *Server {
	stdlog := log.Default().StandardLog(log.StandardLogOptions{
		ForceLevel: log.ErrorLevel,
	})

	mux := BuildMux(state)

	return &Server{
		Server: &http.Server{
			Addr:     state.Config.ListenHTTP,
			Handler:  mux,
			ErrorLog: stdlog,
		},
		State: state,
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
