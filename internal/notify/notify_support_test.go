package notify

import ()

// validateChannel checks a channel against the built-in kinds.
func validateChannel(c Channel) error {
	return validateWith(builtinRegistry(), c)
}
