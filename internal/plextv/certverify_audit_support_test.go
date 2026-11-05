package plextv

import ()

// setTrustDial replaces the TLS dial VerifyTrust uses. Tests only.
func (m *CertManager) setTrustDial(fn trustDialFunc) {
	m.mu.Lock()
	m.trustDial = fn
	m.mu.Unlock()
}
