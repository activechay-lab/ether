package livetv

// VirtualDASHError is a virtual channel that could not produce DASH.
// Plex turns it into a decision document. A bare 503 is what Plex Web
// reports as s1001 (Network), which hides the reason and blocks fallback.
type VirtualDASHError struct {
	Err error
	// Message is the client-facing reason when it is more specific than Err.
	Message string
}

func (e *VirtualDASHError) Error() string {
	if e == nil {
		return "DASH is not available for this channel"
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "DASH is not available for this channel"
}

func (e *VirtualDASHError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
