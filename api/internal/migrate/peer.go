package migrate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/store"
	"github.com/dylanwongtencent/daedal/api/internal/vmm"
)

var peerHTTP = &http.Client{Timeout: 10 * time.Minute}

type PeerInitRequest struct {
	Spec         vmm.VMSpec `json:"spec"`
	Mode         Mode       `json:"mode"`
	TransferDisk bool       `json:"transfer_disk"`
}

type peerInitResponse struct {
	Token string `json:"token"`
}

type peerCompleteRequest struct {
	Resume bool `json:"resume"`
}

type peerCompleteResponse struct {
	VMID string `json:"vm_id"`
}

func (m *Manager) migrateRemote(vm *vmm.VM, mig *Migration, req Request) error {
	base := strings.TrimSuffix(req.Target, "/")
	dirs, err := m.migDirs(vm, mig.ID)
	if err != nil {
		m.rollback(vm, false)
		return err
	}
	clock := m.newPhaseClock(mig)
	old := vm.Handle

	initBody, _ := json.Marshal(PeerInitRequest{Spec: vm.Spec, Mode: mig.Mode, TransferDisk: req.TransferDisk})
	var initResp peerInitResponse
	if err := peerJSON("POST", base+"/v1/peer/migrations", initBody, &initResp); err != nil {
		m.rollback(vm, false)
		return fmt.Errorf("peer init: %w", err)
	}
	tok := initResp.Token
	abort := func() {
		reqAbort, _ := http.NewRequest("DELETE", base+"/v1/peer/migrations/"+tok, nil)
		resp, err := peerHTTP.Do(reqAbort)
		if err == nil {
			resp.Body.Close()
		}
	}

	var downtime time.Duration
	if mig.Mode == ModePrecopy {
		t := time.Now()
		if err := old.Pause(); err != nil {
			abort()
			m.markDead(vm, err)
			return err
		}
		if err := old.Snapshot(vmm.SnapFull, vmm.SnapshotPaths{StatePath: dirs.baseState, MemPath: dirs.baseMem}); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		if err := old.Resume(); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		downtime += time.Since(t)
		clock.mark("precopy_base_snapshot", t)

		streamStart := time.Now()
		if err := m.streamFiles(base, tok, map[string]string{
			"base.mem":     dirs.baseMem,
			"base.vmstate": dirs.baseState,
		}); err != nil {
			abort()
			m.rollback(vm, false)
			return err
		}
		if req.TransferDisk {
			if err := m.streamFiles(base, tok, map[string]string{"rootfs.ext4": vm.Spec.RootfsPath}); err != nil {
				abort()
				m.rollback(vm, false)
				return err
			}
		}
		clock.mark("stream_base", streamStart)

		t2 := time.Now()
		if err := old.Pause(); err != nil {
			abort()
			m.markDead(vm, err)
			return err
		}
		if err := old.Snapshot(vmm.SnapDiff, vmm.SnapshotPaths{StatePath: dirs.diffState, MemPath: dirs.diffMem}); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		streamDiffStart := time.Now()
		if err := m.streamFiles(base, tok, map[string]string{
			"diff.mem":     dirs.diffMem,
			"diff.vmstate": dirs.diffState,
		}); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		clock.mark("stream_diff", streamDiffStart)
		completeStart := time.Now()
		var comp peerCompleteResponse
		compBody, _ := json.Marshal(peerCompleteRequest{Resume: true})
		if err := peerJSON("POST", base+"/v1/peer/migrations/"+tok+"/complete", compBody, &comp); err != nil {
			abort()
			m.rollback(vm, true)
			return fmt.Errorf("peer complete: %w", err)
		}
		clock.mark("peer_restore", completeStart)
		downtime += time.Since(t2)
		mig.DestVMID = comp.VMID
	} else {
		t := time.Now()
		if err := old.Pause(); err != nil {
			abort()
			m.markDead(vm, err)
			return err
		}
		if err := old.Snapshot(vmm.SnapFull, vmm.SnapshotPaths{StatePath: dirs.baseState, MemPath: dirs.baseMem}); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		streamStart := time.Now()
		files := map[string]string{
			"base.mem":     dirs.baseMem,
			"base.vmstate": dirs.baseState,
		}
		if req.TransferDisk {
			files["rootfs.ext4"] = vm.Spec.RootfsPath
		}
		if err := m.streamFiles(base, tok, files); err != nil {
			abort()
			m.rollback(vm, true)
			return err
		}
		clock.mark("stream", streamStart)
		completeStart := time.Now()
		var comp peerCompleteResponse
		compBody, _ := json.Marshal(peerCompleteRequest{Resume: true})
		if err := peerJSON("POST", base+"/v1/peer/migrations/"+tok+"/complete", compBody, &comp); err != nil {
			abort()
			m.rollback(vm, true)
			return fmt.Errorf("peer complete: %w", err)
		}
		clock.mark("peer_restore", completeStart)
		downtime = time.Since(t)
		mig.DestVMID = comp.VMID
	}
	mig.DowntimeMs = float64(downtime.Microseconds()) / 1000
	_ = old.Kill()
	vm.State = vmm.StateStopped
	vm.LastError = ""
	m.Store.Add(vm)
	_ = os.RemoveAll(dirs.root)
	return nil
}

func peerJSON(method, url string, body []byte, out any) error {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := peerHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, url, resp.StatusCode, string(data))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (m *Manager) streamFiles(base, tok string, files map[string]string) error {
	for name, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		sum := sha256.New()
		st, err := f.Stat()
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(sum, f); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			f.Close()
			return err
		}
		req, err := http.NewRequest("PUT", base+"/v1/peer/migrations/"+tok+"/files/"+name, f)
		if err != nil {
			f.Close()
			return err
		}
		req.ContentLength = st.Size()
		req.Header.Set("X-Content-Sha256", hex.EncodeToString(sum.Sum(nil)))
		resp, err := peerHTTP.Do(req)
		f.Close()
		if err != nil {
			return fmt.Errorf("stream %s: %w", name, err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("stream %s: %d %s", name, resp.StatusCode, string(data))
		}
	}
	return nil
}

type IncomingMigration struct {
	Token        string     `json:"token"`
	Spec         vmm.VMSpec `json:"spec"`
	Mode         Mode       `json:"mode"`
	TransferDisk bool       `json:"transfer_disk"`
	Dir          string     `json:"dir"`
	ReceivedAt   time.Time  `json:"received_at"`
}

func (m *Manager) PeerInit(req PeerInitRequest) (*IncomingMigration, error) {
	tok := store.NewID("in")
	dir := filepath.Join(m.StateDir, "incoming", tok)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	inc := &IncomingMigration{
		Token:        tok,
		Spec:         req.Spec,
		Mode:         req.Mode,
		TransferDisk: req.TransferDisk,
		Dir:          dir,
		ReceivedAt:   time.Now(),
	}
	m.mu.Lock()
	if m.incoming == nil {
		m.incoming = map[string]*IncomingMigration{}
	}
	m.incoming[tok] = inc
	m.mu.Unlock()
	return inc, nil
}

func (m *Manager) PeerGet(tok string) (*IncomingMigration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inc, ok := m.incoming[tok]
	return inc, ok
}

func (m *Manager) PeerAbort(tok string) {
	m.mu.Lock()
	inc := m.incoming[tok]
	delete(m.incoming, tok)
	m.mu.Unlock()
	if inc != nil {
		_ = os.RemoveAll(inc.Dir)
	}
}

var allowedPeerFiles = map[string]bool{
	"base.mem": true, "base.vmstate": true,
	"diff.mem": true, "diff.vmstate": true,
	"rootfs.ext4": true,
}

func (m *Manager) PeerReceiveFile(inc *IncomingMigration, name string, body io.Reader, wantSha string) error {
	if !allowedPeerFiles[name] {
		return fmt.Errorf("file name %q not allowed", name)
	}
	dst := filepath.Join(inc.Dir, name)
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	sum := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, sum), body); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	f.Close()
	if wantSha != "" {
		got := hex.EncodeToString(sum.Sum(nil))
		if got != wantSha {
			_ = os.Remove(dst)
			return fmt.Errorf("sha256 mismatch for %s: got %s want %s", name, got, wantSha)
		}
	}
	return nil
}

func (m *Manager) PeerComplete(inc *IncomingMigration, resume bool) (*vmm.VM, error) {
	spec := inc.Spec
	backend, ok := m.Backends[spec.Backend]
	if !ok {
		return nil, fmt.Errorf("backend %q not available on this host", spec.Backend)
	}
	baseMem := filepath.Join(inc.Dir, "base.mem")
	baseState := filepath.Join(inc.Dir, "base.vmstate")
	diffMem := filepath.Join(inc.Dir, "diff.mem")
	diffState := filepath.Join(inc.Dir, "diff.vmstate")
	statePath := baseState
	if _, err := os.Stat(diffMem); err == nil {
		if spec.Backend != "mock" {
			if err := MergeDiffOntoBase(baseMem, diffMem); err != nil {
				return nil, fmt.Errorf("merge diff: %w", err)
			}
		}
		statePath = diffState
	}
	if inc.TransferDisk {
		spec.RootfsPath = filepath.Join(inc.Dir, "rootfs.ext4")
	}
	id := store.NewID("vm")
	dir := filepath.Join(m.StateDir, "vms", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	vm := &vmm.VM{
		ID:        id,
		Spec:      spec,
		State:     vmm.StateCreating,
		Dir:       dir,
		CreatedAt: time.Now(),
	}
	inst, err := backend.Restore(vm, vmm.SnapshotPaths{StatePath: statePath, MemPath: baseMem}, resume)
	if err != nil {
		return nil, fmt.Errorf("restore: %w", err)
	}
	vm.Handle = inst
	vm.PID = inst.PID()
	if resume {
		vm.State = vmm.StateRunning
	} else {
		vm.State = vmm.StatePaused
	}
	_ = os.WriteFile(filepath.Join(dir, ".backing"), []byte(inc.Dir), 0o644)
	m.Store.Add(vm)
	m.mu.Lock()
	delete(m.incoming, inc.Token)
	m.mu.Unlock()
	return vm, nil
}
