package overlays

import (
	"context"

	_ "image/png"
)

// composite retains the package's offline rendering entry point.
func (s *Service) composite(data []byte, labels []string, settings Settings) ([]byte, string, error) {
	return s.compositeContext(context.Background(), data, labels, settings)
}
