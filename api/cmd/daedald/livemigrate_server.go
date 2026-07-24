package main

import (
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/livemigrate"
	"github.com/dylanwongtencent/daedal/api/internal/metrics"
)

// liveMigrateServe exposes the live-migration orchestrator as a small REST API:
//
//	POST /v1/migrations  -> run one live migration, return its timings
//	GET  /v1/metrics     -> blackout p50/p95/p99 across migrations run so far
//	GET  /healthz
//
// The environment (binaries, guest images) is fixed at startup; each request
// triggers a migration and records its blackout.
func liveMigrateServe(args []string) {
	fs := flag.NewFlagSet("livemigrate-serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7040", "listen address")
	fcBin := fs.String("firecracker", "/usr/local/bin/firecracker", "firecracker binary")
	uffdBin := fs.String("uffd-handler", "", "uffd_on_demand_handler binary")
	kernel := fs.String("kernel", "", "guest kernel path")
	rootfs := fs.String("rootfs", "", "guest rootfs path")
	shared := fs.String("shared-dir", "/dev/shm/daedal-lm", "shared tmpfs dir")
	target := fs.Float64("target-ms", 30, "blackout SLA in ms")
	_ = fs.Parse(args)

	base := livemigrate.Config{
		FirecrackerBin: *fcBin,
		UffdHandlerBin: *uffdBin,
		SharedDir:      *shared,
		Guest: livemigrate.GuestSpec{
			KernelPath:     *kernel,
			RootfsPath:     *rootfs,
			BootArgs:       "console=ttyS0 reboot=k panic=1 pci=off init=/init",
			Vcpus:          1,
			MemMiB:         32,
			RootfsReadOnly: true,
		},
		PrecopyRounds: 3,
		MemBackend:    "File",
	}

	var mu sync.Mutex
	var blackouts []float64

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/migrations", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MemMiB     int64  `json:"mem_mib"`
			MemBackend string `json:"mem_backend"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		cfg := base
		cfg.WorkDir = "/tmp/daedal-lm-" + time.Now().Format("150405.000")
		if req.MemMiB > 0 {
			cfg.Guest.MemMiB = req.MemMiB
		}
		if req.MemBackend != "" {
			cfg.MemBackend = req.MemBackend
		}
		res, handles, err := livemigrate.Run(cfg)
		if handles != nil {
			defer handles.Close()
		}
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		mu.Lock()
		blackouts = append(blackouts, res.BlackoutMs)
		mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"blackout_ms": res.BlackoutMs,
			"phases_ms":   res.PhasesMs,
			"precopy_ms":  res.PrecopyMs,
			"target_ms":   *target,
			"pass":        res.BlackoutMs <= *target,
		})
	})
	mux.HandleFunc("GET /v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		p := metrics.ComputePercentiles(append([]float64(nil), blackouts...))
		n := len(blackouts)
		mu.Unlock()
		writeJSON(w, 200, map[string]any{
			"migrations":     n,
			"blackout_ms":    p,
			"target_ms":      *target,
			"p99_within_sla": p.P99 <= *target,
		})
	})

	log.Printf("live-migration API listening on %s (target blackout %.0fms)", *listen, *target)
	log.Fatal(http.ListenAndServe(*listen, mux))
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
