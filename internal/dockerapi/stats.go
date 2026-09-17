package dockerapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/developerabdan/dps/internal/model"
)

// apiStats is the wire shape of GET /containers/{id}/stats. Only the counters
// dps draws are declared.
type apiStats struct {
	Read     time.Time `json:"read"`
	CPUStats struct {
		CPUUsage struct {
			TotalUsage  uint64   `json:"total_usage"`
			PercpuUsage []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	BlkioStats struct {
		IOServiceBytesRecursive []blkioEntry `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type blkioEntry struct {
	Op    string `json:"op"`
	Value uint64 `json:"value"`
}

// Stats reads one sample of a container's counters.
//
// one-shot=1 makes the daemon answer at once. Without it the daemon waits
// about a second to fill precpu_stats, which is one second for each container
// on every poll. dps does not read precpu_stats: it keeps the previous sample
// itself and takes the difference.
func (c *Client) Stats(ctx context.Context, id string) (model.Sample, error) {
	q := url.Values{}
	q.Set("stream", "0")
	q.Set("one-shot", "1")
	rc, err := c.get(ctx, "/containers/"+url.PathEscape(id)+"/stats", q)
	if err != nil {
		return model.Sample{}, err
	}
	defer rc.Close()

	var raw apiStats
	if err := json.NewDecoder(rc).Decode(&raw); err != nil {
		return model.Sample{}, err
	}
	return convertStats(raw), nil
}

// StatsWorkers is how many stats requests run at the same time. Each one
// answers in tens of milliseconds, so eight clear a large host inside one poll
// without opening a connection for every container.
const StatsWorkers = 8

// StatsFor reads a sample for each id at the same time. A container that
// stopped or was removed after the list was fetched fails its request; it is
// left out of the result rather than failing the others.
func (c *Client) StatsFor(ctx context.Context, ids []string) map[string]model.Sample {
	out := make(map[string]model.Sample, len(ids))
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		slot = make(chan struct{}, StatsWorkers)
	)
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slot <- struct{}{}
			defer func() { <-slot }()
			s, err := c.Stats(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			out[id] = s
			mu.Unlock()
		}()
	}
	wg.Wait()
	return out
}

func convertStats(r apiStats) model.Sample {
	s := model.Sample{
		Read:       r.Read,
		CPUTotal:   r.CPUStats.CPUUsage.TotalUsage,
		SystemCPU:  r.CPUStats.SystemCPUUsage,
		OnlineCPUs: r.CPUStats.OnlineCPUs,
		MemUsage:   memUsage(r.MemoryStats.Usage, r.MemoryStats.Stats),
		MemLimit:   r.MemoryStats.Limit,
		PIDs:       r.PidsStats.Current,
	}
	// Daemons before API 1.41 do not send online_cpus; the per-CPU list has
	// one entry for each CPU the container can use.
	if s.OnlineCPUs == 0 {
		s.OnlineCPUs = len(r.CPUStats.CPUUsage.PercpuUsage)
	}
	// cgroup v1 writes "Read" and "Write" once for each device, cgroup v2
	// writes "read" and "write". Both are summed over every device.
	for _, e := range r.BlkioStats.IOServiceBytesRecursive {
		switch {
		case strings.EqualFold(e.Op, "read"):
			s.DiskRead += e.Value
		case strings.EqualFold(e.Op, "write"):
			s.DiskWrite += e.Value
		}
	}
	for _, n := range r.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	return s
}

// memUsage removes the inactive page cache from the usage, as `docker stats`
// does. The kernel can take that memory back at any time, and counting it
// makes a container that read a large file look like it leaks. cgroup v1
// names the counter total_inactive_file, cgroup v2 inactive_file.
func memUsage(usage uint64, stats map[string]uint64) uint64 {
	if v, ok := stats["total_inactive_file"]; ok {
		if v < usage {
			return usage - v
		}
		return usage
	}
	if v, ok := stats["inactive_file"]; ok && v < usage {
		return usage - v
	}
	return usage
}
