//go:build linux || darwin

package term

import (
	"os"
	"os/signal"
	"syscall"
)

// NotifyResize delivers a value on ch each time the terminal window changes
// size. Stop it with signal.Stop.
func NotifyResize(ch chan<- os.Signal) { signal.Notify(ch, syscall.SIGWINCH) }
