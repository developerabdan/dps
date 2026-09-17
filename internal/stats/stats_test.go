package stats

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/developerabdan/dps/internal/model"
)

var t0 = time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)

func sample(at time.Duration, cpu, system uint64) model.Sample {
	return model.Sample{Read: t0.Add(at), CPUTotal: cpu, SystemCPU: system, OnlineCPUs: 8}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestCPUNeedsTwoSamples(t *testing.T) {
	var s Series
	s.Add(sample(0, 1_000, 100_000))
	if s.CPU.Len() != 0 {
		t.Fatalf("one sample gave %d CPU values", s.CPU.Len())
	}
	// The container used 1/80 of the host's CPU time on an 8-CPU host, which
	// is 10% of one CPU.
	s.Add(sample(2*time.Second, 2_000, 180_000))
	got, ok := s.CPU.Last()
	if !ok || !near(got, 10) {
		t.Errorf("cpu = %v, %v; want 10", got, ok)
	}
}

func TestRestartedContainerSkipsOneSample(t *testing.T) {
	var s Series
	s.Add(sample(0, 5_000, 100_000))
	s.Add(sample(2*time.Second, 50, 180_000)) // counters began again
	if s.CPU.Len() != 0 {
		t.Fatalf("a reset counter gave a CPU value: %v", s.CPU.Values())
	}
	s.Add(sample(4*time.Second, 1_050, 260_000))
	if got, _ := s.CPU.Last(); !near(got, 10) {
		t.Errorf("cpu after the reset = %v, want 10", got)
	}
}

func TestRatesUseTheDaemonClock(t *testing.T) {
	var s Series
	a := sample(0, 0, 100)
	a.NetRx, a.DiskWrite = 1_000, 0
	b := sample(4*time.Second, 0, 200)
	b.NetRx, b.DiskWrite = 9_000, 4_000
	s.Add(a)
	s.Add(b)
	if got, _ := s.NetRx.Last(); !near(got, 2_000) {
		t.Errorf("rx = %v B/s, want 2000", got)
	}
	if got, _ := s.DiskWrite.Last(); !near(got, 1_000) {
		t.Errorf("write = %v B/s, want 1000", got)
	}
}

func TestMemoryIsRecordedAtOnce(t *testing.T) {
	var s Series
	s.Add(model.Sample{Read: t0, MemUsage: 42})
	if got, ok := s.Mem.Last(); !ok || got != 42 {
		t.Errorf("mem = %v, %v; want 42 from the first sample", got, ok)
	}
}

func TestRingKeepsTheNewest(t *testing.T) {
	var r Ring
	for i := 0; i < History+5; i++ {
		r.Push(float64(i))
	}
	v := r.Values()
	if len(v) != History || v[0] != 5 || v[len(v)-1] != History+4 {
		t.Errorf("len %d, first %v, last %v", len(v), v[0], v[len(v)-1])
	}
	if last, _ := r.Last(); last != History+4 {
		t.Errorf("last %v", last)
	}
}

func TestSparkIsAlwaysTheSameWidth(t *testing.T) {
	for _, n := range []int{0, 3, 10, 40} {
		values := make([]float64, n)
		if got := len([]rune(Spark(values, 10, 10))); got != 10 {
			t.Errorf("%d values: spark is %d cells", n, got)
		}
	}
}

func TestSparkNeverUsesTheFullBlock(t *testing.T) {
	got := Spark([]float64{0, 5, 10, 500}, 4, 10)
	if strings.ContainsRune(got, '█') {
		t.Errorf("spark %q uses the full block, so rows would touch", got)
	}
	if got != "▁▄▇▇" {
		t.Errorf("spark = %q, want ▁▄▇▇", got)
	}
}

func TestChartStacksEighths(t *testing.T) {
	// Two lines give sixteen steps: 10 of 16 fills the bottom line and two
	// steps of the top one.
	lines := Chart([]float64{10, 0}, 2, 2, 16)
	if lines[0] != "▂ " || lines[1] != "█ " {
		t.Errorf("chart = %q", lines)
	}
	// A value too small for one step still shows.
	if got := Chart([]float64{0.01}, 1, 1, 100)[0]; got != "▁" {
		t.Errorf("small value drew %q", got)
	}
}

func TestTopHasAFloor(t *testing.T) {
	if got := Top(10, 1, []float64{0.1, 0.3}); got != 10 {
		t.Errorf("top = %v, want the floor", got)
	}
	if got := Top(10, 1.25, []float64{4}, []float64{40}); got != 50 {
		t.Errorf("top = %v, want 50", got)
	}
}

func TestPercentIsSixCells(t *testing.T) {
	for _, v := range []float64{0, 0.4, 38.14, 99.96, 812.3, 3199} {
		if got := len(Percent(v)); got != 6 {
			t.Errorf("Percent(%v) = %q, %d cells", v, Percent(v), got)
		}
	}
}

func TestLongGapStartsTheHistoryAgain(t *testing.T) {
	var s Series
	s.Add(sample(0, 0, 100))
	s.Add(sample(2*time.Second, 10, 200))
	if s.CPU.Len() != 1 {
		t.Fatalf("cpu history %d, want 1", s.CPU.Len())
	}
	s.Add(sample(2*time.Minute, 20, 300))
	if s.CPU.Len() != 0 || s.Mem.Len() != 1 {
		t.Errorf("after a 2m gap: %d cpu values, %d memory values; want 0 and 1", s.CPU.Len(), s.Mem.Len())
	}
}
