package config

import "encoding/json"

// Missing enabled fields in older configuration mean enabled.
func (p *UsenetProvider) UnmarshalJSON(b []byte) error {
	type plain UsenetProvider
	v := plain{Enabled: true}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = UsenetProvider(v)
	return nil
}
func (p *Indexer) UnmarshalJSON(b []byte) error {
	type plain Indexer
	v := plain{Enabled: true}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = Indexer(v)
	return nil
}
func (p *DebridProvider) UnmarshalJSON(b []byte) error {
	type plain DebridProvider
	v := plain{Enabled: true}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*p = DebridProvider(v)
	return nil
}
