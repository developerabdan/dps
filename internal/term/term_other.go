//go:build !linux && !darwin

package term

import "os"

// Released targets are linux and darwin only. Elsewhere the kernel is not
// asked and Width falls back, so the package still builds.
func ioctlWidth(_ *os.File) int { return 0 }

// ioctlIsTerm is false off the release targets, so those platforms always take
// the plain path rather than opening an interactive view that has not been
// tested there.
func ioctlIsTerm(_ *os.File) bool { return false }
