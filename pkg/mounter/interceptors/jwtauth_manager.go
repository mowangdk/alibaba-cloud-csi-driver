package interceptors

import (
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// refresherManager tracks live credential refreshers so a driver can stop and
// wait for all of them during Terminate. Its shutdown is bounded by a timeout,
// mirroring server.MountMonitorManager.WaitForAllMonitoring, so a hung HTTP
// call can never stall driver Terminate indefinitely.
type refresherManager struct {
	mu         sync.Mutex
	refreshers map[*credentialRefresher]struct{}
}

func newRefresherManager() *refresherManager {
	return &refresherManager{
		refreshers: make(map[*credentialRefresher]struct{}),
	}
}

func (m *refresherManager) add(r *credentialRefresher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshers[r] = struct{}{}
}

func (m *refresherManager) remove(r *credentialRefresher) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.refreshers, r)
}

// StopAll stops every tracked refresher, cleans up its credential files, and
// waits for them to exit, bounded by timeout. Refreshers are stopped
// concurrently; each refresher's own Stop is itself bounded, so this returns
// promptly.
func (m *refresherManager) StopAll(timeout time.Duration) {
	m.mu.Lock()
	pending := make([]*credentialRefresher, 0, len(m.refreshers))
	for r := range m.refreshers {
		pending = append(pending, r)
	}
	m.mu.Unlock()

	if len(pending) == 0 {
		return
	}

	done := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, r := range pending {
			wg.Add(1)
			go func(r *credentialRefresher) {
				defer wg.Done()
				r.Stop()
				r.Cleanup()
				m.remove(r)
			}(r)
		}
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		klog.V(4).InfoS("All jwtauth refreshers stopped")
	case <-time.After(timeout):
		klog.Warningf("StopAll jwtauth refreshers timed out after %v", timeout)
	}
}

// StopAllRefreshers stops all tracked jwtauth credential refreshers.
// Intended to be called from a driver's Terminate.
func StopAllRefreshers() {
	jwtAuthManager.StopAll(2 * time.Second)
}
