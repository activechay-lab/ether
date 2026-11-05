package personalize

import ()

// prob is the calibrated play probability of a served title.
func (m *rankModel) prob(raw []float32, kind, surface string) float64 {
	return sigmoid(m.logit(raw, kind, surface))
}
