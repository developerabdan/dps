// Package stats turns raw counter samples into the numbers dps draws: CPU
// share, memory in use, and disk and network rates, each with a short history
// for the graphs. Nothing here talks to Docker or to the terminal.
package stats

import (
	"time"

	"github.com/developerabdan/dps/internal/model"
)

// History is how many values each series keeps. At one sample every two
// seconds that is eight minutes, which fills the widest chart the stats view
// draws on a large monitor.
const History = 240

// Ring is a fixed-size history. When it is full, a new value pushes out the
// oldest one.
type Ring struct {
	buf  []float64
	next int
	full bool
}

// Push adds v as the newest value.
func (r *Ring) Push(v float64) {
	if r.buf == nil {
		r.buf = make([]float64, History)
	}
	r.buf[r.next] = v
	r.next = (r.next + 1) % len(r.buf)
	if r.next == 0 {
		r.full = true
	}
}

// Len is how many values the ring holds.
func (r *Ring) Len() int {
	if r.full {
		return len(r.buf)
	}
	return r.next
}

// Values returns the history from oldest to newest.
func (r *Ring) Values() []float64 {
	if !r.full {
		return append([]float64(nil), r.buf[:r.next]...)
	}
	out := make([]float64, 0, len(r.buf))
	out = append(out, r.buf[r.next:]...)
	return append(out, r.buf[:r.next]...)
}

// Last returns the newest value, and false when there is none yet.
func (r *Ring) Last() (float64, bool) {
	if r.Len() == 0 {
		return 0, false
	}
	i := r.next - 1
	if i < 0 {
		i = len(r.buf) - 1
	}
	return r.buf[i], true
}

// MaxGap is the longest pause between two samples that a history can span.
// A chart puts its values side by side as if they were evenly spaced, so after
// a longer pause — the stats view was closed and nothing else sampled the
// container — the old history would join the new one as if no time had passed.
// The history starts again instead.
const MaxGap = 10 * time.Second

// Series is the history of one container.
type Series struct {
	// CPU is in percent of one CPU, as `docker stats` prints it, so a
	// container that uses two full cores reads 200.
	CPU Ring
	// Mem is bytes in use.
	Mem Ring
	// DiskRead, DiskWrite, NetRx and NetTx are bytes per second.
	DiskRead  Ring
	DiskWrite Ring
	NetRx     Ring
	NetTx     Ring

	// Latest holds the newest sample, for the values that are levels rather
	// than something to graph: the memory limit, the CPU count and the
	// process count, and the running totals behind each rate.
	Latest model.Sample

	has bool
}

// Add takes a new sample. Memory is a level, so it is recorded at once. CPU and
// the rates are differences, so they start with the second sample.
func (s *Series) Add(cur model.Sample) {
	if s.has && !cur.Read.IsZero() && !s.Latest.Read.IsZero() && cur.Read.Sub(s.Latest.Read) > MaxGap {
		*s = Series{}
	}
	s.Mem.Push(float64(cur.MemUsage))
	prev, had := s.Latest, s.has
	s.Latest, s.has = cur, true
	if !had {
		return
	}

	// A counter that went down means the container restarted and its counters
	// began again at zero. That pair of samples has no meaning, so it is
	// skipped, and the new sample is the baseline for the next one.
	if cur.CPUTotal >= prev.CPUTotal && cur.SystemCPU > prev.SystemCPU {
		s.CPU.Push(cpuPercent(prev, cur))
	}

	secs := cur.Read.Sub(prev.Read).Seconds()
	if cur.Read.IsZero() || prev.Read.IsZero() || secs <= 0 {
		return
	}
	pushRate(&s.DiskRead, prev.DiskRead, cur.DiskRead, secs)
	pushRate(&s.DiskWrite, prev.DiskWrite, cur.DiskWrite, secs)
	pushRate(&s.NetRx, prev.NetRx, cur.NetRx, secs)
	pushRate(&s.NetTx, prev.NetTx, cur.NetTx, secs)
}

// Span is how much time the CPU history covers.
func (s *Series) Span(interval time.Duration) time.Duration {
	return time.Duration(s.CPU.Len()) * interval
}

// cpuPercent is the formula `docker stats` uses: the container's share of
// the CPU time the whole host spent, times the number of CPUs.
func cpuPercent(prev, cur model.Sample) float64 {
	cpus := cur.OnlineCPUs
	if cpus <= 0 {
		cpus = 1
	}
	used := float64(cur.CPUTotal - prev.CPUTotal)
	total := float64(cur.SystemCPU - prev.SystemCPU)
	return used / total * float64(cpus) * 100
}

func pushRate(r *Ring, prev, cur uint64, secs float64) {
	if cur < prev {
		return
	}
	r.Push(float64(cur-prev) / secs)
}
