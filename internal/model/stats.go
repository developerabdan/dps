package model

import (
	"fmt"
	"strconv"
	"time"
)

// Sample is one reading of a container's resource counters. Every field is a
// running total or a level as the daemon reports it, not a rate: a rate needs
// two samples, and the stats package takes the difference.
type Sample struct {
	// Read is when the daemon took the reading. Rates are divided by the time
	// between two of these rather than by the poll interval, because a slow
	// answer would otherwise show up as a traffic spike.
	Read time.Time

	// CPUTotal is the container's CPU time and SystemCPU the host's, both in
	// nanoseconds. Their two differences give the share of the host.
	CPUTotal   uint64
	SystemCPU  uint64
	OnlineCPUs int

	// MemUsage is without the page cache the kernel can take back, which is
	// the number `docker stats` prints.
	MemUsage uint64
	MemLimit uint64

	DiskRead  uint64
	DiskWrite uint64
	NetRx     uint64
	NetTx     uint64

	PIDs uint64
}

// HumanBytes prints a byte count in SI units, the way Docker does: 1.5MB is
// 1,500,000 bytes. Zero and below print nothing, so a column shows its dash.
func HumanBytes(n int64) string {
	if n <= 0 {
		return ""
	}
	const unit = 1000
	if n < unit {
		return strconv.FormatInt(n, 10) + "B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 3; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "kMGT"[exp])
}
