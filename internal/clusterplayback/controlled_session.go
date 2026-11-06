package clusterplayback

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// controlledSession keeps public retirement on the coordinator for every
// distributed representation, including sessions without a recovery buffer.
// Worker status and transport remain internal; Stop never proves retirement.
type controlledSession struct {
	*remotetranscode.Session
	r *Runtime
}

func (s *controlledSession) EncoderRetirement() core.EncoderRetirement {
	token, _ := s.r.EncoderRetirement(s.ID())
	if token == nil {
		return s.Session
	}
	return token
}
func (s *controlledSession) EncoderDone() <-chan struct{} { return s.EncoderRetirement().EncoderDone() }
func (s *controlledSession) EncoderErr() error            { return s.EncoderRetirement().EncoderErr() }
func (s *controlledSession) Close() error                 { return s.RetireEncoder() }
func (s *controlledSession) RetireEncoder() error {
	s.r.Stop(s.ID())
	return s.EncoderErr()
}
func (s *controlledSession) RetireEncoderContext(ctx context.Context) error {
	s.r.stop(ctx, s.ID())
	return s.EncoderErr()
}
