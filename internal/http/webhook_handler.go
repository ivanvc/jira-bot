package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/github"
	"github.com/ivanvc/jira-bot/internal/executor"
)

// webhookHandler holds the HTTP endpoint to handle GitHub's webhook.
type webhookHandler struct{}

// Registers the handler to be used by an HTTP server.
func (h *webhookHandler) registerHandler(s *Server) {
	http.HandleFunc("/webhooks/github/payload", h.handle(s))
}

// Handles the HTTP request.
func (h *webhookHandler) handle(s *Server) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(req.Body)
		if err != nil {
			log.Error("Error reading request body", "error", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if !h.verifySignature(body, req.Header.Get("X-Hub-Signature-256"), s.State.Config.GitHubWebhookSecret) {
			log.Error("Invalid webhook signature")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch req.Header.Get("X-Github-Event") {
		case "issue_comment":
			var ic github.IssueComment
			if err := json.Unmarshal(body, &ic); err != nil {
				log.Error("Error unmarshalling", "error", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			if ic.Action != "created" {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			if err := executor.Run(req.Context(), s.State, &ic); err != nil {
				log.Error("Error executing webhook", "error", err)
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
		}

		w.WriteHeader(http.StatusOK)
	}
}

func (h *webhookHandler) verifySignature(payload []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}

	signature = strings.TrimPrefix(signature, "sha256=")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	expectedSignature := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}
