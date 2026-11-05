package debrid

import ()

// failureCount reports host's consecutive failures (tests and diagnostics).
func (b *hostBackoff) failureCount(host string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if st := b.hosts[host]; st != nil {
		return st.failures
	}
	return 0
}
