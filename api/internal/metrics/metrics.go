package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type MigrationRecord struct {
	ID         string             `json:"id"`
	VMID       string             `json:"vm_id"`
	Backend    string             `json:"backend"`
	Mode       string             `json:"mode"`
	MemMiB     int64              `json:"mem_mib"`
	OK         bool               `json:"ok"`
	Error      string             `json:"error,omitempty"`
	TotalMs    float64            `json:"total_ms"`
	DowntimeMs float64            `json:"downtime_ms"`
	Phases     map[string]float64 `json:"phases_ms"`
	StartedAt  time.Time          `json:"started_at"`
}

type Recorder struct {
	mu      sync.Mutex
	records []MigrationRecord
	path    string
}

func NewRecorder(stateDir string) *Recorder {
	r := &Recorder{path: filepath.Join(stateDir, "migrations.jsonl")}
	r.load()
	return r
}

func (r *Recorder) load() {
	data, err := os.ReadFile(r.path)
	if err != nil {
		return
	}
	for _, line := range splitLines(data) {
		var rec MigrationRecord
		if json.Unmarshal(line, &rec) == nil {
			r.records = append(r.records, rec)
		}
	}
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			if i > start {
				out = append(out, data[start:i])
			}
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func (r *Recorder) Record(rec MigrationRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	if f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		enc := json.NewEncoder(f)
		_ = enc.Encode(rec)
		f.Close()
	}
}

type Summary struct {
	Count       int                    `json:"count"`
	OKCount     int                    `json:"ok_count"`
	FailCount   int                    `json:"fail_count"`
	TotalMs     Percentiles            `json:"total_ms"`
	DowntimeMs  Percentiles            `json:"downtime_ms"`
	ByBackend   map[string]Percentiles `json:"by_backend_total_ms"`
	HistogramMs map[string]int         `json:"histogram_total_ms"`
}

type Percentiles struct {
	P50 float64 `json:"p50"`
	P95 float64 `json:"p95"`
	P99 float64 `json:"p99"`
	Max float64 `json:"max"`
	Min float64 `json:"min"`
}

func (r *Recorder) Summary() Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Summary{ByBackend: map[string]Percentiles{}, HistogramMs: map[string]int{}}
	var totals, downtimes []float64
	byBackend := map[string][]float64{}
	for _, rec := range r.records {
		s.Count++
		if !rec.OK {
			s.FailCount++
			continue
		}
		s.OKCount++
		totals = append(totals, rec.TotalMs)
		downtimes = append(downtimes, rec.DowntimeMs)
		byBackend[rec.Backend] = append(byBackend[rec.Backend], rec.TotalMs)
		s.HistogramMs[bucket(rec.TotalMs)]++
	}
	s.TotalMs = percentiles(totals)
	s.DowntimeMs = percentiles(downtimes)
	for k, v := range byBackend {
		s.ByBackend[k] = percentiles(v)
	}
	return s
}

func (r *Recorder) Records() []MigrationRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MigrationRecord, len(r.records))
	copy(out, r.records)
	return out
}

func bucket(ms float64) string {
	switch {
	case ms < 500:
		return "lt_500ms"
	case ms < 1000:
		return "500ms_1s"
	case ms < 2000:
		return "1s_2s"
	case ms < 5000:
		return "2s_5s"
	case ms < 10000:
		return "5s_10s"
	case ms < 20000:
		return "10s_20s"
	default:
		return "gte_20s"
	}
}

func ComputePercentiles(vals []float64) Percentiles {
	return percentiles(vals)
}

func percentiles(vals []float64) Percentiles {
	if len(vals) == 0 {
		return Percentiles{}
	}
	sorted := make([]float64, len(vals))
	copy(sorted, vals)
	sort.Float64s(sorted)
	at := func(p float64) float64 {
		idx := int(p*float64(len(sorted))+0.5) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sorted) {
			idx = len(sorted) - 1
		}
		return sorted[idx]
	}
	return Percentiles{
		P50: at(0.50),
		P95: at(0.95),
		P99: at(0.99),
		Max: sorted[len(sorted)-1],
		Min: sorted[0],
	}
}
