package transcode

import ()

func probeArgs(input string, a probeAttempt) []string {
	return probeArgsMode(input, a, false)
}
