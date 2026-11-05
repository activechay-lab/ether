package logredact

import (
	"errors"
	"fmt"
	"net/url"
)

// TransportError copies a net/http URL error through clean. If clean is nil,
// the URL is omitted entirely and prefix names the service. The underlying
// transport error is preserved so errors.Is and timeout checks still work.
func TransportError(err error, prefix string, clean func(string) string) error {
	var ue *url.Error
	if !errors.As(err, &ue) {
		return err
	}
	if clean == nil {
		return fmt.Errorf("%s: %s request: %w", prefix, ue.Op, ue.Err)
	}
	return &url.Error{Op: ue.Op, URL: clean(ue.URL), Err: ue.Err}
}
