package k8s

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// LeaderCallbacks defines what happens on leadership transitions.
type LeaderCallbacks struct {
	OnStartedLeading func(ctx context.Context)
	OnStoppedLeading func()
	OnNewLeader      func(identity string)
}

// LeaderElectorConfig holds leader election parameters.
type LeaderElectorConfig struct {
	LeaseName      string
	LeaseNamespace string
	Identity       string        // pod name
	LeaseDuration  time.Duration // default 15s
	RenewDeadline  time.Duration // default 10s
	RetryPeriod    time.Duration // default 2s
}

// applyDefaults fills in zero-value fields with their defaults.
func (c *LeaderElectorConfig) applyDefaults() {
	if c.LeaseDuration == 0 {
		c.LeaseDuration = 15 * time.Second
	}
	if c.RenewDeadline == 0 {
		c.RenewDeadline = 10 * time.Second
	}
	if c.RetryPeriod == 0 {
		c.RetryPeriod = 2 * time.Second
	}
}

// NewLeaderElector creates a leader elector using a Kubernetes Lease lock.
func NewLeaderElector(client kubernetes.Interface, cfg LeaderElectorConfig, callbacks LeaderCallbacks) (*leaderelection.LeaderElector, error) {
	cfg.applyDefaults()

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      cfg.LeaseName,
			Namespace: cfg.LeaseNamespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: cfg.Identity,
		},
	}

	return leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.RenewDeadline,
		RetryPeriod:     cfg.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: callbacks.OnStartedLeading,
			OnStoppedLeading: callbacks.OnStoppedLeading,
			OnNewLeader:      callbacks.OnNewLeader,
		},
	})
}
