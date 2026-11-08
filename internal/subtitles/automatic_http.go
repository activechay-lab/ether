package subtitles

import (
	"context"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/retryafter"
)

type automaticHTTPKey struct{}

const automaticStatusTimeout = 2 * time.Second

func automaticBudget(ctx context.Context) *AutomaticBudget {
	b, _ := ctx.Value(automaticHTTPKey{}).(*AutomaticBudget)
	return b
}

func (s *Service) automaticPause(ctx context.Context, response *http.Response) error {
	b := automaticBudget(ctx)
	if b == nil || response.StatusCode != http.StatusTooManyRequests {
		return nil
	}
	wait, ok := retryafter.Parse(response.Header.Get("Retry-After"), s.now(), 0)
	if !ok {
		wait = time.Minute
	}
	pauseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), automaticStatusTimeout)
	defer cancel()
	return b.Pause(pauseCtx, s.now().Add(wait))
}
