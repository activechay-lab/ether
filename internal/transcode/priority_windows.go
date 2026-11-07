//go:build windows

package transcode

import "golang.org/x/sys/windows"

// lowerEncoderPriority puts the guardian below normal; a process it creates
// inherits a below-normal priority class.
func lowerEncoderPriority() {
	_ = windows.SetPriorityClass(windows.CurrentProcess(), windows.BELOW_NORMAL_PRIORITY_CLASS)
}
