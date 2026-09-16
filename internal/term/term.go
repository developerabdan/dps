// Package term reports terminal facts without pulling in a dependency.
// Mode selection hangs off IsTTY: dps prints a plain table whenever stdout
// is not a character device, so pipes and redirects behave like every other
// unix tool.
package term

import (
	"os"
	"strconv"
)

// FallbackWidth is used when stdout is not a terminal, so piped output is
// stable and diffable rather than varying with whoever ran it.
const FallbackWidth = 100

// IsTTY reports whether f is attached to a terminal.
//
// The character-device bit alone is not enough: /dev/null carries it too, so
// `dps > /dev/null` would try to open the interactive view and fail on
// /dev/tty. Asking the kernel for a window size is what actually separates a
// terminal from any other character device.
func IsTTY(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	return ioctlIsTerm(f)
}

// Width returns the usable column count for f. COLUMNS wins when set so the
// value can be forced in tests and scripts, then the kernel is asked, and
// FallbackWidth closes it out.
func Width(f *os.File) int {
	if s := os.Getenv("COLUMNS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
	}
	if w := ioctlWidth(f); w > 0 {
		return w
	}
	return FallbackWidth
}
