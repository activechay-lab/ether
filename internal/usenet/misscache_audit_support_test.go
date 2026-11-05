package usenet

import ()

func (m *missCache) len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.order.Len()
}
