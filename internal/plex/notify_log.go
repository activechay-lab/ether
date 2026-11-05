package plex

import (
	"fmt"
	"net/http"

	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Console subscribers receive actual request diagnostics. Query strings and
// headers are excluded because they can contain credentials or user input.
func (s *Server) notifyRequestLog(r *http.Request, status int) {
	if s.notifier == nil {
		return
	}
	if status == 0 {
		status = http.StatusOK
	}
	s.publishNotification(model.NotificationContainer{
		Type: "log", Size: 1,
		LogSentNotification: []model.LogSentNotification{{
			Level: 3, Thread: "0", Time: s.now().Format("Jan 02, 2006 15:04:05.000"),
			Message: fmt.Sprintf("Request: %s %s [%d]", r.Method, r.URL.EscapedPath(), status),
		}},
	}, 0)
}
