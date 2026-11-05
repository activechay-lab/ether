package plex

import (
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// publish delivers to every interested subscriber, dropping for slow ones.
func (n *notifier) publish(nc model.NotificationContainer, userID int64) {
	if msg, ok := encodeNotification(nc); ok {
		n.deliver(msg, userID)
	}
}
