//go:build !windows

package transcode

import "syscall"

// encoderBackgroundNice gives a live output about three times the CPU share
// of an on-demand encode when both compete (CFS weights 1024 vs 335).
const encoderBackgroundNice = 5

// lowerEncoderPriority lowers the calling thread; on Linux nice is per
// thread, and a fork inherits it. Raising it back needs no privilege loss:
// lowering is always permitted.
func lowerEncoderPriority() { _ = syscall.Setpriority(syscall.PRIO_PROCESS, 0, encoderBackgroundNice) }
