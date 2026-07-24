package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/metrics"
	"github.com/dylanwongtencent/daedal/api/internal/migrate"
	"github.com/dylanwongtencent/daedal/api/internal/store"
	"github.com/dylanwongtencent/daedal/api/internal/vmm"
)

type Server struct {
	Store    *store.Store
	Manager  *migrate.Manager
	Recorder *metrics.Recorder
	Caps     vmm.Capabilities
	StateDir string
	Started  time.Time
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, apiError{Error: err.Error(), Code: code})
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/capabilities", s.handleCapabilities)
	mux.HandleFunc("GET /spec", s.handleSpec)
	mux.HandleFunc("POST /v1/vms", s.handleCreateVM)
	mux.HandleFunc("GET /v1/vms", s.handleListVMs)
	mux.HandleFunc("GET /v1/vms/{id}", s.handleGetVM)
	mux.HandleFunc("DELETE /v1/vms/{id}", s.handleDeleteVM)
	mux.HandleFunc("POST /v1/vms/{id}/actions", s.handleVMAction)
	mux.HandleFunc("POST /v1/vms/{id}/migrate", s.handleMigrate)
	mux.HandleFunc("GET /v1/vms/{id}/console", s.handleConsole)
	mux.HandleFunc("GET /v1/migrations", s.handleListMigrations)
	mux.HandleFunc("GET /v1/migrations/{id}", s.handleGetMigration)
	mux.HandleFunc("GET /v1/metrics", s.handleMetrics)
	mux.HandleFunc("POST /v1/peer/migrations", s.handlePeerInit)
	mux.HandleFunc("PUT /v1/peer/migrations/{tok}/files/{name}", s.handlePeerFile)
	mux.HandleFunc("POST /v1/peer/migrations/{tok}/complete", s.handlePeerComplete)
	mux.HandleFunc("DELETE /v1/peer/migrations/{tok}", s.handlePeerAbort)
	return logMiddleware(mux)
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"ok": true, "uptime_s": int(time.Since(s.Started).Seconds())})
}

func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Caps)
}

func (s *Server) handleSpec(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(openapiSpec)
}

type createVMRequest struct {
	Name            string `json:"name"`
	Backend         string `json:"backend,omitempty"`
	Vcpus           int64  `json:"vcpus,omitempty"`
	MemMiB          int64  `json:"mem_mib,omitempty"`
	KernelPath      string `json:"kernel_path,omitempty"`
	RootfsPath      string `json:"rootfs_path,omitempty"`
	BootArgs        string `json:"boot_args,omitempty"`
	TrackDirtyPages *bool  `json:"track_dirty_pages,omitempty"`
}

func (s *Server) handleCreateVM(w http.ResponseWriter, r *http.Request) {
	var req createVMRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	backendName := req.Backend
	if backendName == "" || backendName == "auto" {
		backendName = s.Caps.DefaultName
	}
	backend, ok := s.Manager.Backends[backendName]
	if !ok {
		writeErr(w, 400, "unknown_backend", fmt.Errorf("backend %q not configured; have %v", backendName, s.Caps.Backends))
		return
	}
	spec := backend.DefaultSpec()
	spec.Name = req.Name
	spec.Backend = backendName
	if req.Vcpus > 0 {
		spec.Vcpus = req.Vcpus
	}
	if req.MemMiB > 0 {
		spec.MemMiB = req.MemMiB
	}
	if req.KernelPath != "" {
		spec.KernelPath = req.KernelPath
	}
	if req.RootfsPath != "" {
		spec.RootfsPath = req.RootfsPath
	}
	if req.BootArgs != "" {
		spec.BootArgs = req.BootArgs
	}
	if req.TrackDirtyPages != nil {
		spec.TrackDirtyPages = *req.TrackDirtyPages
	}
	if err := vmm.ValidateSpec(spec); err != nil {
		writeErr(w, 400, "invalid_spec", err)
		return
	}
	id := store.NewID("vm")
	dir := filepath.Join(s.StateDir, "vms", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	vm := &vmm.VM{ID: id, Spec: spec, State: vmm.StateCreating, Dir: dir, CreatedAt: time.Now()}
	s.Store.Add(vm)
	inst, err := backend.Boot(vm)
	if err != nil {
		vm.State = vmm.StateError
		vm.LastError = err.Error()
		s.Store.Add(vm)
		writeErr(w, 500, "boot_failed", err)
		return
	}
	vm.Handle = inst
	vm.PID = inst.PID()
	vm.State = vmm.StateRunning
	s.Store.Add(vm)
	writeJSON(w, 201, vm)
}

func (s *Server) handleListVMs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Store.List())
}

func (s *Server) handleGetVM(w http.ResponseWriter, r *http.Request) {
	vm, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	writeJSON(w, 200, vm)
}

func (s *Server) handleDeleteVM(w http.ResponseWriter, r *http.Request) {
	vm, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	if vm.State == vmm.StateMigrating {
		writeErr(w, 409, "migrating", fmt.Errorf("vm %s is mid-migration", vm.ID))
		return
	}
	s.Manager.DestroyVM(vm)
	writeJSON(w, 200, map[string]string{"deleted": vm.ID})
}

type vmActionRequest struct {
	Type string `json:"type"`
}

func (s *Server) handleVMAction(w http.ResponseWriter, r *http.Request) {
	vm, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	var req vmActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	switch req.Type {
	case "pause":
		if _, err := s.Store.CompareAndSetState(vm.ID, vmm.StateRunning, vmm.StatePaused); err != nil {
			writeErr(w, 409, "bad_state", err)
			return
		}
		if err := vm.Handle.Pause(); err != nil {
			writeErr(w, 500, "pause_failed", err)
			return
		}
	case "resume":
		if _, err := s.Store.CompareAndSetState(vm.ID, vmm.StatePaused, vmm.StateRunning); err != nil {
			writeErr(w, 409, "bad_state", err)
			return
		}
		if err := vm.Handle.Resume(); err != nil {
			writeErr(w, 500, "resume_failed", err)
			return
		}
	default:
		writeErr(w, 400, "unknown_action", fmt.Errorf("action %q not supported", req.Type))
		return
	}
	writeJSON(w, 200, vm)
}

func (s *Server) handleMigrate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Store.Get(id); err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	var req migrate.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeErr(w, 400, "bad_request", err)
		return
	}
	mig, err := s.Manager.Migrate(id, req)
	if err != nil {
		status := 500
		if strings.Contains(err.Error(), "expected running") || strings.Contains(err.Error(), "is migrating") {
			status = 409
		}
		if mig != nil {
			writeJSON(w, status, mig)
			return
		}
		writeErr(w, status, "migrate_failed", err)
		return
	}
	writeJSON(w, 200, mig)
}

func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	vm, err := s.Store.Get(r.PathValue("id"))
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	data, err := os.ReadFile(filepath.Join(vm.Dir, "console.log"))
	if err != nil {
		writeErr(w, 404, "no_console", err)
		return
	}
	const tail = 64 * 1024
	if len(data) > tail {
		data = data[len(data)-tail:]
	}
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write(data)
}

func (s *Server) handleListMigrations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Manager.List())
}

func (s *Server) handleGetMigration(w http.ResponseWriter, r *http.Request) {
	mig, ok := s.Manager.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "not_found", fmt.Errorf("migration %q not found", r.PathValue("id")))
		return
	}
	writeJSON(w, 200, mig)
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Recorder.Summary())
}

func (s *Server) handlePeerInit(w http.ResponseWriter, r *http.Request) {
	var req migrate.PeerInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	inc, err := s.Manager.PeerInit(req)
	if err != nil {
		writeErr(w, 500, "peer_init_failed", err)
		return
	}
	writeJSON(w, 201, map[string]string{"token": inc.Token})
}

func (s *Server) handlePeerFile(w http.ResponseWriter, r *http.Request) {
	inc, ok := s.Manager.PeerGet(r.PathValue("tok"))
	if !ok {
		writeErr(w, 404, "not_found", fmt.Errorf("incoming migration %q not found", r.PathValue("tok")))
		return
	}
	if err := s.Manager.PeerReceiveFile(inc, r.PathValue("name"), r.Body, r.Header.Get("X-Content-Sha256")); err != nil {
		writeErr(w, 400, "receive_failed", err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handlePeerComplete(w http.ResponseWriter, r *http.Request) {
	inc, ok := s.Manager.PeerGet(r.PathValue("tok"))
	if !ok {
		writeErr(w, 404, "not_found", fmt.Errorf("incoming migration %q not found", r.PathValue("tok")))
		return
	}
	var req struct {
		Resume bool `json:"resume"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeErr(w, 400, "bad_request", err)
		return
	}
	vm, err := s.Manager.PeerComplete(inc, req.Resume)
	if err != nil {
		writeErr(w, 500, "peer_complete_failed", err)
		return
	}
	writeJSON(w, 200, map[string]string{"vm_id": vm.ID})
}

func (s *Server) handlePeerAbort(w http.ResponseWriter, r *http.Request) {
	s.Manager.PeerAbort(r.PathValue("tok"))
	writeJSON(w, 200, map[string]bool{"ok": true})
}
