package transition

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
	internalhttp "github.com/ivanvc/jira-bot/internal/http"
)

// ClientFactory constructs the Jira OAuth2 client and returns a shutdown function.
// The shutdown function stops background goroutines (leader election, token polling).
type ClientFactory func(cfg common.Config, tokenData k8s.TokenData) (common.JiraClientInterface, func(), error)

// MuxBuilder constructs the oauth2-mode ServeMux.
type MuxBuilder func(state *common.State) *http.ServeMux

// TransitionCoordinator orchestrates the in-process transition from oauth2-setup
// mode to oauth2 mode. It uses a sync.Mutex to serialize attempts and a boolean
// flag to track whether a successful transition has occurred.
type TransitionCoordinator struct {
	mu           sync.Mutex
	transitioned bool

	state         *common.State
	mux           *internalhttp.SwitchableMux
	clientFactory ClientFactory
	muxBuilder    MuxBuilder
	logger        *log.Logger
}

// NewTransitionCoordinator creates a new coordinator wired to the given state,
// switchable mux, client factory, mux builder, and logger.
func NewTransitionCoordinator(
	state *common.State,
	mux *internalhttp.SwitchableMux,
	clientFactory ClientFactory,
	muxBuilder MuxBuilder,
	logger *log.Logger,
) *TransitionCoordinator {
	return &TransitionCoordinator{
		state:         state,
		mux:           mux,
		clientFactory: clientFactory,
		muxBuilder:    muxBuilder,
		logger:        logger,
	}
}

// Transition attempts the setup → oauth2 transition.
// Returns nil on success or if already transitioned.
// Returns an error if the transition fails (state remains unchanged).
func (tc *TransitionCoordinator) Transition(tokenData k8s.TokenData) error {
	start := time.Now()

	tc.mu.Lock()
	defer tc.mu.Unlock()

	// Already transitioned — return success without repeating the work.
	if tc.transitioned {
		return nil
	}

	tc.logger.Info("Transition started", "from", "oauth2-setup", "to", "oauth2")

	// Validate tokenData: both RefreshToken and CloudID must be non-empty.
	if tokenData.RefreshToken == "" {
		return fmt.Errorf("transition aborted: TokenData has empty RefreshToken")
	}
	if tokenData.CloudID == "" {
		return fmt.Errorf("transition aborted: TokenData has empty CloudID")
	}

	// Construct the Jira client. This is the only fallible step after validation.
	// If it fails, no state has been modified — rollback is trivial (do nothing).
	jiraClient, _, err := tc.clientFactory(tc.state.Config, tokenData)
	if err != nil {
		tc.logger.Error("Transition failed during Jira client construction", "error", err)
		return fmt.Errorf("client construction failed: %w", err)
	}

	// --- From here, all operations are infallible in-memory assignments ---

	// Update state fields.
	tc.state.Config.AuthMode = "oauth2"
	tc.state.Config.TokenData = tokenData
	tc.state.JiraClient = jiraClient

	// Build new mux for oauth2 mode and swap atomically.
	newMux := tc.muxBuilder(tc.state)
	tc.mux.Swap(newMux)

	// Mark transition as complete.
	tc.transitioned = true

	elapsed := time.Since(start)
	tc.logger.Info("Transition complete, bot is now in oauth2 mode", "elapsed_ms", elapsed.Milliseconds())

	return nil
}
