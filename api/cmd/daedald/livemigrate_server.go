package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/livemigrate"
	"github.com/dylanwongtencent/daedal/api/internal/metrics"
)

type migrationBroadcaster struct {
	mu   sync.Mutex
	subs map[chan any]struct{}
}

func newMigrationBroadcaster() *migrationBroadcaster {
	return &migrationBroadcaster{subs: make(map[chan any]struct{})}
}

func (b *migrationBroadcaster) Subscribe() chan any {
	ch := make(chan any, 16)
	b.mu.Lock()
	b.subs[ch] = struct{}{}
	b.mu.Unlock()
	return ch
}

func (b *migrationBroadcaster) Unsubscribe(ch chan any) {
	b.mu.Lock()
	if _, ok := b.subs[ch]; ok {
		delete(b.subs, ch)
		close(ch)
	}
	b.mu.Unlock()
}

func (b *migrationBroadcaster) Publish(event any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- event:
		default:
		}
	}
}

func migrationsEventsHandler(bc *migrationBroadcaster) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch := bc.Subscribe()
		defer bc.Unsubscribe(ch)

		for {
			select {
			case event, ok := <-ch:
				if !ok {
					return
				}
				buf, err := json.Marshal(event)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", buf)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}
}

type hostRegistry struct {
	mu    sync.Mutex
	hosts map[string]string
}

func newHostRegistry() *hostRegistry {
	return &hostRegistry{hosts: make(map[string]string)}
}

func (h *hostRegistry) Register(id, nodeID string) {
	h.mu.Lock()
	h.hosts[id] = nodeID
	h.mu.Unlock()
}

type hostEntry struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
}

func (h *hostRegistry) List() []hostEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hostEntry, 0, len(h.hosts))
	for id, nodeID := range h.hosts {
		out = append(out, hostEntry{ID: id, NodeID: nodeID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type migrationInFlight struct {
	mu     sync.Mutex
	active map[string]bool
}

func newMigrationInFlight() *migrationInFlight {
	return &migrationInFlight{active: make(map[string]bool)}
}

func (m *migrationInFlight) TryStart(pairKey string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active[pairKey] {
		return false
	}
	m.active[pairKey] = true
	return true
}

func (m *migrationInFlight) Finish(pairKey string) {
	m.mu.Lock()
	delete(m.active, pairKey)
	m.mu.Unlock()
}

// liveMigrateServe exposes the live-migration orchestrator as a small REST API:
//
//	POST /v1/migrations                -> run one live migration, return its timings
//	GET  /v1/migrations/events         -> SSE stream of real migration lifecycle events
//	GET  /v1/metrics                   -> blackout p50/p95/p99 across migrations run so far
//	POST /v1/hosts/{id}/vnc-endpoint   -> register a host's VNC tunnel node id
//	GET  /v1/hosts                     -> list registered hosts
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
	bc := newMigrationBroadcaster()
	hosts := newHostRegistry()
	inFlight := newMigrationInFlight()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/migrations/events", migrationsEventsHandler(bc))
	mux.HandleFunc("POST /v1/hosts/{id}/vnc-endpoint", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req struct {
			NodeID string `json:"node_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		hosts.Register(id, req.NodeID)
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /v1/hosts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, hosts.List())
	})
	mux.HandleFunc("POST /v1/migrations", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MemMiB     int64  `json:"mem_mib"`
			MemBackend string `json:"mem_backend"`
			From       string `json:"from"`
			To         string `json:"to"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.From == "" {
			req.From = "local-src"
		}
		if req.To == "" {
			req.To = "local-dst"
		}
		pairKey := req.From + "->" + req.To

		if !inFlight.TryStart(pairKey) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "migration already in flight for " + pairKey,
			})
			return
		}
		defer inFlight.Finish(pairKey)

		migrationID := pairKey + "-" + fmt.Sprintf("%d", time.Now().UnixNano())

		cfg := base
		cfg.WorkDir = "/tmp/daedal-lm-" + time.Now().Format("150405.000")
		if req.MemMiB > 0 {
			cfg.Guest.MemMiB = req.MemMiB
		}
		if req.MemBackend != "" {
			cfg.MemBackend = req.MemBackend
		}
		cfg.Progress = func(ev livemigrate.Event) {
			switch ev.Kind {
			case livemigrate.EventCutoverStart:
				bc.Publish(map[string]any{
					"type":         "migration_progress",
					"migration_id": migrationID,
					"phase":        "cutover",
				})
			case livemigrate.EventLoadResumeComplete:
				bc.Publish(map[string]any{
					"type":         "migration_complete",
					"migration_id": migrationID,
					"blackout_ms":  ev.BlackoutMs,
					"pass":         ev.BlackoutMs <= *target,
				})
			case livemigrate.EventRollback:
				bc.Publish(map[string]any{
					"type":         "migration_failed",
					"migration_id": migrationID,
					"error":        ev.Err.Error(),
				})
			}
		}

		bc.Publish(map[string]any{
			"type":         "migration_start",
			"migration_id": migrationID,
			"from":         req.From,
			"to":           req.To,
		})
		bc.Publish(map[string]any{
			"type":         "migration_progress",
			"migration_id": migrationID,
			"phase":        "precopy",
		})

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
			"migration_id": migrationID,
			"blackout_ms":  res.BlackoutMs,
			"phases_ms":    res.PhasesMs,
			"precopy_ms":   res.PrecopyMs,
			"target_ms":    *target,
			"pass":         res.BlackoutMs <= *target,
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
	log.Fatal(http.ListenAndServe(*listen, withCORS(mux)))
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
