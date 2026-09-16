//go:build linux

package term

import (
	"os"
	"syscall"
	"unsafe"
)

const tiocgwinsz = 0x5413

type winsize struct {
	Row, Col, Xpixel, Ypixel uint16
}

// ioctlIsTerm reports whether the window-size request succeeds, which only a
// real terminal answers.
func ioctlIsTerm(f *os.File) bool {
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		tiocgwinsz,
		uintptr(unsafe.Pointer(&ws)),
	)
	return errno == 0
}

func ioctlWidth(f *os.File) int {
	var ws winsize
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		tiocgwinsz,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
