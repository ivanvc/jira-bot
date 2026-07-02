package k8s_test

import (
	"github.com/ivanvc/jira-bot/internal/adapters/k8s"
	"github.com/ivanvc/jira-bot/internal/common"
)

// Compile-time check that K8sUserTokenStore satisfies common.UserTokenStore.
var _ common.UserTokenStore = (*k8s.K8sUserTokenStore)(nil)
