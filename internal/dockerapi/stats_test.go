package dockerapi

import (
	"context"
	"net/http"
	"testing"
)

// cgroupV2Stats is a trimmed copy of a Docker 28 answer on Docker Desktop.
const cgroupV2Stats = `{
  "read": "2026-09-17T02:41:22.744183576Z",
  "pids_stats": {"current": 5},
  "blkio_stats": {"io_service_bytes_recursive": [
    {"major": 254, "minor": 0, "op": "read", "value": 9801728},
    {"major": 254, "minor": 0, "op": "write", "value": 4096}
  ]},
  "cpu_stats": {"cpu_usage": {"total_usage": 2150373000}, "system_cpu_usage": 27636610000000, "online_cpus": 8},
  "memory_stats": {"usage": 14966784, "limit": 8220114944, "stats": {"inactive_file": 729088, "anon": 3715072}},
  "networks": {
    "eth0": {"rx_bytes": 3546, "tx_bytes": 126},
    "eth1": {"rx_bytes": 1000, "tx_bytes": 20}
  }
}`

func TestStatsAsksForOneShot(t *testing.T) {
	var query string
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v"+PreferredAPI+"/containers/abc/stats" {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		query = r.URL.RawQuery
		w.Write([]byte(cgroupV2Stats))
	}))

	s, err := c.Stats(context.Background(), "abc")
	if err != nil {
		t.Fatal(err)
	}
	// Without one-shot the daemon waits a second for each container.
	if query != "one-shot=1&stream=0" {
		t.Errorf("query %q", query)
	}
	if s.Read.IsZero() || s.CPUTotal != 2150373000 || s.SystemCPU != 27636610000000 || s.OnlineCPUs != 8 {
		t.Errorf("cpu fields %+v", s)
	}
	if s.MemUsage != 14966784-729088 || s.MemLimit != 8220114944 {
		t.Errorf("memory %d of %d, want the page cache removed", s.MemUsage, s.MemLimit)
	}
	if s.DiskRead != 9801728 || s.DiskWrite != 4096 {
		t.Errorf("disk %d read, %d write", s.DiskRead, s.DiskWrite)
	}
	if s.NetRx != 4546 || s.NetTx != 146 {
		t.Errorf("network %d rx, %d tx, want every interface summed", s.NetRx, s.NetTx)
	}
	if s.PIDs != 5 {
		t.Errorf("pids %d", s.PIDs)
	}
}

func TestStatsReadsCgroupV1Names(t *testing.T) {
	raw := apiStats{}
	raw.CPUStats.CPUUsage.PercpuUsage = []uint64{1, 2, 3, 4}
	raw.MemoryStats.Usage = 1000
	raw.MemoryStats.Stats = map[string]uint64{"total_inactive_file": 300, "inactive_file": 900}
	raw.BlkioStats.IOServiceBytesRecursive = []blkioEntry{
		{Op: "Read", Value: 10},
		{Op: "Read", Value: 5},
		{Op: "Total", Value: 99},
	}

	s := convertStats(raw)
	if s.OnlineCPUs != 4 {
		t.Errorf("cpus %d, want the per-CPU count when online_cpus is missing", s.OnlineCPUs)
	}
	if s.MemUsage != 700 {
		t.Errorf("memory %d, want total_inactive_file removed on cgroup v1", s.MemUsage)
	}
	if s.DiskRead != 15 {
		t.Errorf("disk read %d, want Read summed over devices and Total ignored", s.DiskRead)
	}
}

func TestStatsForSkipsContainersThatFail(t *testing.T) {
	c := fakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v"+PreferredAPI+"/containers/gone/stats" {
			http.Error(w, `{"message":"No such container: gone"}`, http.StatusNotFound)
			return
		}
		w.Write([]byte(cgroupV2Stats))
	}))

	got := c.StatsFor(context.Background(), []string{"a", "gone", "b"})
	if len(got) != 2 {
		t.Fatalf("got samples for %d containers, want 2", len(got))
	}
	if _, ok := got["gone"]; ok {
		t.Error("a failed request still produced a sample")
	}
}
