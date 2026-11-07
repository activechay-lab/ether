package transcode

import "runtime"

// continuousHostShare is the share of the host's cores continuous (Live TV)
// outputs may use together; the rest stays free for on-demand playback and
// the server itself.
const continuousHostShare = 0.6

// ContinuousBudget reports the CPU cores continuous outputs may use on this
// host, and whether a hardware video encoder takes their encode work.
func (m *Manager) ContinuousBudget() (cores float64, hardware bool) {
	hw := m.HWAccel()
	return float64(runtime.NumCPU()) * continuousHostShare, hw.Name != "" && hw.Name != AccelNone
}
