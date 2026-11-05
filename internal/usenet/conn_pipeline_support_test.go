package usenet

import (
	"context"

	"github.com/braedonsaunders/ether/internal/yenc"
)

// bodyPipeline sends a bounded window before reading responses, decoding each
// directly from the socket buffer. Delivery is incremental: a fast first article
// never waits for later articles. The caller owns c exclusively throughout.
// A framing/transport failure poisons the remaining window; a complete missing
// response or corrupt yEnc body does not. Every index is delivered exactly once.
func (c *Conn) bodyPipeline(ctx context.Context, ids []string, opt *yenc.DecodeOptions, deliver func(int, *yenc.Part, error)) {
	c.bodyPipelineStarted(ctx, ids, opt, nil, deliver)
}
