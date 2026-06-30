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

// New returns a new Server.
func NewServer(common *common.State) *Server {
	stdlog := log.Default().StandardLog(log.StandardLogOptions{
		ForceLevel: log.ErrorLevel,
	})
	return &Server{&http.Server{
		Addr:     common.Config.ListenHTTP,
		ErrorLog: stdlog,
	}, common}
}

// Starts the HTTP server.
func (s *Server) Start() error {
	log.Info("Starting HTTP server", "listen", s.Addr)
	s.registerHandlers()

	if err := s.ListenAndServe(); err != nil {
		log.Error("Error starting Web Server", "error", err)
		return err
	}

	return nil
}

func (s *Server) registerHandlers() {
	(&webhookHandler{}).registerHandler(s)
	(&statusHandler{}).registerHandler()

	// Register OAuth setup endpoints only when client credentials are present
	// but no refresh token is configured yet (initial setup mode).
	if s.Config.AuthMode == "oauth2-setup" {
		handler := &oauthSetupHandler{
			clientID:     s.Config.JiraClientID,
			clientSecret: s.Config.JiraClientSecret,
			callbackURL:  s.Config.OAuthCallbackURL,
			persistence:  s.State.TokenPersistenceAdapter,
		}
		handler.registerHandler()
		http.HandleFunc("/", handler.handleRootSetup)
	} else {
		http.HandleFunc("/", handleRootStatus)
	}
}
