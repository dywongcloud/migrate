package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
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

type migrationProgressPublisher struct {
	bc          *migrationBroadcaster
	migrationID string
	targetMs    float64
	failedSent  bool
}

func (p *migrationProgressPublisher) handle(ev livemigrate.Event) {
	switch ev.Kind {
	case livemigrate.EventCutoverStart:
		p.bc.Publish(map[string]any{
			"type":         "migration_progress",
			"migration_id": p.migrationID,
			"phase":        "cutover",
		})
	case livemigrate.EventLoadResumeComplete:
		p.bc.Publish(map[string]any{
			"type":         "migration_complete",
			"migration_id": p.migrationID,
			"blackout_ms":  ev.BlackoutMs,
			"pass":         ev.BlackoutMs <= p.targetMs,
		})
	case livemigrate.EventRollback:
		p.failedSent = true
		p.bc.Publish(map[string]any{
			"type":         "migration_failed",
			"migration_id": p.migrationID,
			"error":        ev.Err.Error(),
		})
	}
}

func (p *migrationProgressPublisher) publishFailure(err error) {
	if p.failedSent {
		return
	}
	p.failedSent = true
	p.bc.Publish(map[string]any{
		"type":         "migration_failed",
		"migration_id": p.migrationID,
		"error":        err.Error(),
	})
}

func newMigrationID(from, to string) string {
	return from + "->" + to + "-" + fmt.Sprintf("%d", time.Now().UnixNano())
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func liveMigrateServe(args []string) {
	fs := flag.NewFlagSet("livemigrate-serve", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7040", "listen address")
	fcBin := fs.String("firecracker", "/usr/local/bin/firecracker", "firecracker binary")
	uffdBin := fs.String("uffd-handler", "", "uffd_on_demand_handler binary")
	kernel := fs.String("kernel", "", "guest kernel path")
	rootfs := fs.String("rootfs", "", "guest rootfs path")
	shared := fs.String("shared-dir", "/dev/shm/daedal-lm", "shared tmpfs dir")
	target := fs.Float64("target-ms", 30, "blackout SLA in ms")
	precopyRounds := fs.Int("precopy-rounds", 3, "diff-snapshot passes that refresh the base memfile before the cutover")
	memBackend := fs.String("mem-backend", "File", "destination memory backend: File or Uffd")
	persistent := fs.Bool("persistent-guest", false, "boot ONE guest at startup and live-migrate that same running guest between the two hosts on every request")
	guestRootfs := fs.String("guest-rootfs", "", "rootfs for the persistent guest (defaults to -rootfs)")
	guestMemMiB := fs.Int64("guest-mem-mib", 1024, "persistent guest RAM in MiB")
	guestVcpus := fs.Int64("guest-vcpus", 2, "persistent guest vcpu count")
	guestBootArgs := fs.String("guest-boot-args", "console=ttyS0 reboot=k panic=1 pci=off net.ifnames=0 biosdevname=0 root=/dev/vda rw init=/sbin/init", "persistent guest kernel boot args")
	guestIface := fs.String("guest-iface", "eth0", "persistent guest NIC id")
	guestMAC := fs.String("guest-mac", "06:00:AC:14:00:03", "persistent guest MAC, kept across hosts")
	guestRootfsRO := fs.Bool("guest-rootfs-ro", false, "open the persistent guest rootfs read-only")
	hostAName := fs.String("host-a", "host-a", "name of the first host")
	hostBName := fs.String("host-b", "host-b", "name of the second host")
	hostATap := fs.String("host-a-tap", "tap-desk-a", "host tap the persistent guest NIC binds to on host-a")
	hostBTap := fs.String("host-b-tap", "tap-desk-b", "host tap the persistent guest NIC binds to on host-b")
	sessionWorkDir := fs.String("session-work-dir", "/tmp/daedal-lm-session", "scratch dir for the persistent session's per-host API sockets and serial logs")
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
		PrecopyRounds: *precopyRounds,
		MemBackend:    *memBackend,
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("listen %s: %v", *listen, err)
	}

	var session *livemigrate.Session
	if *persistent {
		cfg := base
		cfg.WorkDir = *sessionWorkDir
		cfg.Guest = livemigrate.GuestSpec{
			KernelPath:     *kernel,
			RootfsPath:     firstNonEmpty(*guestRootfs, *rootfs),
			BootArgs:       *guestBootArgs,
			Vcpus:          *guestVcpus,
			MemMiB:         *guestMemMiB,
			RootfsReadOnly: *guestRootfsRO,
			Net:            &livemigrate.NetIface{IfaceID: *guestIface, GuestMAC: *guestMAC},
		}
		started, serr := livemigrate.StartSession(cfg, [2]livemigrate.HostSpec{
			{Name: *hostAName, Tap: *hostATap},
			{Name: *hostBName, Tap: *hostBTap},
		})
		if serr != nil {
			log.Fatalf("persistent guest: %v", serr)
		}
		session = started
		log.Printf("persistent guest booted on %s: rootfs=%s mem=%dMiB vcpus=%d mac=%s tap=%s api_sock=%s",
			session.CurrentHost(), cfg.Guest.RootfsPath, cfg.Guest.MemMiB, cfg.Guest.Vcpus,
			*guestMAC, *hostATap, session.GuestSock())
	}

	var mu sync.Mutex
	var blackouts []float64
	recordBlackout := func(v float64) {
		mu.Lock()
		blackouts = append(blackouts, v)
		mu.Unlock()
	}
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
	mux.HandleFunc("GET /v1/migrations/current-host", func(w http.ResponseWriter, r *http.Request) {
		if session == nil {
			writeJSON(w, 404, map[string]string{"error": "no persistent guest, start daedald with -persistent-guest"})
			return
		}
		writeJSON(w, 200, map[string]string{
			"host": session.CurrentHost(),
			"next": session.NextHost(),
		})
	})
	mux.HandleFunc("GET /v1/migrations/guest", func(w http.ResponseWriter, r *http.Request) {
		if session == nil {
			writeJSON(w, 404, map[string]string{"error": "no persistent guest, start daedald with -persistent-guest"})
			return
		}
		cfg, err := session.GuestMachineConfig()
		if err != nil {
			writeJSON(w, 502, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{
			"host":           session.CurrentHost(),
			"next":           session.NextHost(),
			"api_sock":       session.GuestSock(),
			"machine_config": json.RawMessage(cfg),
		})
	})
	mux.HandleFunc("POST /v1/migrations", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			MemMiB     int64  `json:"mem_mib"`
			MemBackend string `json:"mem_backend"`
			From       string `json:"from"`
			To         string `json:"to"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		if session != nil {
			if !inFlight.TryStart("persistent-guest") {
				writeJSON(w, http.StatusConflict, map[string]string{
					"error": "migration already in flight for the persistent guest",
				})
				return
			}
			defer inFlight.Finish("persistent-guest")

			from, to := session.CurrentHost(), session.NextHost()
			pub := &migrationProgressPublisher{bc: bc, migrationID: newMigrationID(from, to), targetMs: *target}
			bc.Publish(map[string]any{
				"type":         "migration_start",
				"migration_id": pub.migrationID,
				"from":         from,
				"to":           to,
			})
			bc.Publish(map[string]any{
				"type":         "migration_progress",
				"migration_id": pub.migrationID,
				"phase":        "precopy",
			})

			res, err := session.Migrate(pub.handle)
			if err != nil {
				pub.publishFailure(err)
				writeJSON(w, 500, map[string]string{"error": err.Error()})
				return
			}
			recordBlackout(res.BlackoutMs)
			writeJSON(w, 200, map[string]any{
				"migration_id":  pub.migrationID,
				"from":          from,
				"to":            to,
				"current_host":  session.CurrentHost(),
				"guest_mem_mib": *guestMemMiB,
				"blackout_ms":   res.BlackoutMs,
				"phases_ms":     res.PhasesMs,
				"precopy_ms":    res.PrecopyMs,
				"target_ms":     *target,
				"pass":          res.BlackoutMs <= *target,
			})
			return
		}

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

		pub := &migrationProgressPublisher{bc: bc, migrationID: newMigrationID(req.From, req.To), targetMs: *target}

		cfg := base
		cfg.WorkDir = "/tmp/daedal-lm-" + time.Now().Format("150405.000")
		if req.MemMiB > 0 {
			cfg.Guest.MemMiB = req.MemMiB
		}
		if req.MemBackend != "" {
			cfg.MemBackend = req.MemBackend
		}
		cfg.Progress = pub.handle

		bc.Publish(map[string]any{
			"type":         "migration_start",
			"migration_id": pub.migrationID,
			"from":         req.From,
			"to":           req.To,
		})
		bc.Publish(map[string]any{
			"type":         "migration_progress",
			"migration_id": pub.migrationID,
			"phase":        "precopy",
		})

		res, handles, err := livemigrate.Run(cfg)
		if handles != nil {
			defer handles.Close()
		}
		if err != nil {
			pub.publishFailure(err)
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		recordBlackout(res.BlackoutMs)
		writeJSON(w, 200, map[string]any{
			"migration_id": pub.migrationID,
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

	if session != nil {
		log.Printf("live-migration API listening on %s (persistent guest on %s, target blackout %.0fms)",
			*listen, session.CurrentHost(), *target)
	} else {
		log.Printf("live-migration API listening on %s (target blackout %.0fms)", *listen, *target)
	}
	log.Fatal(http.Serve(ln, withCORS(mux)))
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
