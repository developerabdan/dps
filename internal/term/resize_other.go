//go:build !linux && !darwin

package term

import "os"

// NotifyResize never fires off the release targets, which have no SIGWINCH.
func NotifyResize(_ chan<- os.Signal) {}
