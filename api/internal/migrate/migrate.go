package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/metrics"
	"github.com/dylanwongtencent/daedal/api/internal/store"
	"github.com/dylanwongtencent/daedal/api/internal/vmm"
)

type Mode string

const (
	ModeCold    Mode = "cold"
	ModePrecopy Mode = "precopy"
)

type Request struct {
	Target       string `json:"target"`
	Mode         Mode   `json:"mode,omitempty"`
	TransferDisk bool   `json:"transfer_disk,omitempty"`
}

type Migration struct {
	ID         string             `json:"id"`
	VMID       string             `json:"vm_id"`
	DestVMID   string             `json:"dest_vm_id,omitempty"`
	Backend    string             `json:"backend"`
	Mode       Mode               `json:"mode"`
	Target     string             `json:"target"`
	Status     string             `json:"status"`
	Error      string             `json:"error,omitempty"`
	Phases     map[string]float64 `json:"phases_ms"`
	TotalMs    float64            `json:"total_ms"`
	DowntimeMs float64            `json:"downtime_ms"`
	StartedAt  time.Time          `json:"started_at"`
	FinishedAt time.Time          `json:"finished_at,omitzero"`
}

type Manager struct {
	Store    *store.Store
	Backends map[string]vmm.Backend
	Recorder *metrics.Recorder
	StateDir string

	mu         sync.Mutex
	migrations map[string]*Migration
	incoming   map[string]*IncomingMigration
}

func NewManager(st *store.Store, backends map[string]vmm.Backend, rec *metrics.Recorder, stateDir string) *Manager {
	return &Manager{
		Store:      st,
		Backends:   backends,
		Recorder:   rec,
		StateDir:   stateDir,
		migrations: map[string]*Migration{},
	}
}

func (m *Manager) Get(id string) (*Migration, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mig, ok := m.migrations[id]
	return mig, ok
}

func (m *Manager) List() []*Migration {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Migration, 0, len(m.migrations))
	for _, mig := range m.migrations {
		out = append(out, mig)
	}
	return out
}

type phaseClock struct {
	mig   *Migration
	start time.Time
}

func (m *Manager) newPhaseClock(mig *Migration) *phaseClock {
	return &phaseClock{mig: mig, start: time.Now()}
}

func (c *phaseClock) mark(name string, since time.Time) {
	c.mig.Phases[name] = float64(time.Since(since).Microseconds()) / 1000
}

func (m *Manager) Migrate(vmID string, req Request) (*Migration, error) {
	if req.Mode == "" {
		req.Mode = ModePrecopy
	}
	if req.Mode != ModeCold && req.Mode != ModePrecopy {
		return nil, fmt.Errorf("unknown migration mode %q", req.Mode)
	}
	if req.Target == "" {
		req.Target = "local"
	}
	vm, err := m.Store.CompareAndSetState(vmID, vmm.StateRunning, vmm.StateMigrating)
	if err != nil {
		return nil, err
	}
	if req.Mode == ModePrecopy && !vm.Spec.TrackDirtyPages {
		req.Mode = ModeCold
	}
	mig := &Migration{
		ID:        store.NewID("mig"),
		VMID:      vm.ID,
		Backend:   vm.Spec.Backend,
		Mode:      req.Mode,
		Target:    req.Target,
		Status:    "running",
		Phases:    map[string]float64{},
		StartedAt: time.Now(),
	}
	m.mu.Lock()
	m.migrations[mig.ID] = mig
	m.mu.Unlock()

	if req.Target == "local" {
		err = m.migrateLocal(vm, mig)
	} else {
		err = m.migrateRemote(vm, mig, req)
	}
	mig.FinishedAt = time.Now()
	mig.TotalMs = float64(mig.FinishedAt.Sub(mig.StartedAt).Microseconds()) / 1000
	if err != nil {
		mig.Status = "failed"
		mig.Error = err.Error()
	} else {
		mig.Status = "succeeded"
	}
	m.Recorder.Record(metrics.MigrationRecord{
		ID:         mig.ID,
		VMID:       mig.VMID,
		Backend:    mig.Backend,
		Mode:       string(mig.Mode),
		MemMiB:     vm.Spec.MemMiB,
		OK:         err == nil,
		Error:      mig.Error,
		TotalMs:    mig.TotalMs,
		DowntimeMs: mig.DowntimeMs,
		Phases:     mig.Phases,
		StartedAt:  mig.StartedAt,
	})
	if err != nil {
		return mig, err
	}
	return mig, nil
}

type migDirs struct {
	root      string
	baseState string
	baseMem   string
	diffState string
	diffMem   string
}

func (m *Manager) migDirs(vm *vmm.VM, migID string) (migDirs, error) {
	root := filepath.Join(vm.Dir, migID)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return migDirs{}, err
	}
	return migDirs{
		root:      root,
		baseState: filepath.Join(root, "base.vmstate"),
		baseMem:   filepath.Join(root, "base.mem"),
		diffState: filepath.Join(root, "diff.vmstate"),
		diffMem:   filepath.Join(root, "diff.mem"),
	}, nil
}

func (m *Manager) migrateLocal(vm *vmm.VM, mig *Migration) error {
	backend, ok := m.Backends[vm.Spec.Backend]
	if !ok {
		m.rollback(vm, false)
		return fmt.Errorf("backend %q not available", vm.Spec.Backend)
	}
	dirs, err := m.migDirs(vm, mig.ID)
	if err != nil {
		m.rollback(vm, false)
		return err
	}
	clock := m.newPhaseClock(mig)
	old := vm.Handle
	var downtime time.Duration

	restorePaths := vmm.SnapshotPaths{StatePath: dirs.baseState, MemPath: dirs.baseMem}
	if mig.Mode == ModePrecopy {
		t := time.Now()
		if err := old.Pause(); err != nil {
			m.markDead(vm, err)
			return fmt.Errorf("precopy pause: %w", err)
		}
		if err := old.Snapshot(vmm.SnapFull, vmm.SnapshotPaths{StatePath: dirs.baseState, MemPath: dirs.baseMem}); err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("precopy full snapshot: %w", err)
		}
		if err := old.Resume(); err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("precopy resume: %w", err)
		}
		downtime += time.Since(t)
		clock.mark("precopy_base_snapshot", t)

		t2 := time.Now()
		if err := old.Pause(); err != nil {
			m.markDead(vm, err)
			return fmt.Errorf("final pause: %w", err)
		}
		if err := old.Snapshot(vmm.SnapDiff, vmm.SnapshotPaths{StatePath: dirs.diffState, MemPath: dirs.diffMem}); err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("diff snapshot: %w", err)
		}
		mergeStart := time.Now()
		if vm.Spec.Backend != "mock" {
			if err := MergeDiffOntoBase(dirs.baseMem, dirs.diffMem); err != nil {
				m.rollback(vm, true)
				return fmt.Errorf("merge diff: %w", err)
			}
		}
		clock.mark("merge_diff", mergeStart)
		restorePaths = vmm.SnapshotPaths{StatePath: dirs.diffState, MemPath: dirs.baseMem}
		restoreStart := time.Now()
		newInst, err := backend.Restore(vm, restorePaths, true)
		if err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("restore: %w", err)
		}
		clock.mark("restore", restoreStart)
		downtime += time.Since(t2)
		m.swapInstance(vm, old, newInst, dirs.root)
	} else {
		t := time.Now()
		if err := old.Pause(); err != nil {
			m.markDead(vm, err)
			return fmt.Errorf("pause: %w", err)
		}
		snapStart := time.Now()
		if err := old.Snapshot(vmm.SnapFull, restorePaths); err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("full snapshot: %w", err)
		}
		clock.mark("snapshot", snapStart)
		restoreStart := time.Now()
		newInst, err := backend.Restore(vm, restorePaths, true)
		if err != nil {
			m.rollback(vm, true)
			return fmt.Errorf("restore: %w", err)
		}
		clock.mark("restore", restoreStart)
		downtime = time.Since(t)
		m.swapInstance(vm, old, newInst, dirs.root)
	}
	mig.DowntimeMs = float64(downtime.Microseconds()) / 1000
	mig.DestVMID = vm.ID
	return nil
}

func (m *Manager) swapInstance(vm *vmm.VM, old, newInst vmm.Instance, backingDir string) {
	_ = old.Kill()
	prevBacking := vm.Dir + "/.backing"
	if prev, err := os.ReadFile(prevBacking); err == nil {
		prevDir := string(prev)
		if prevDir != "" && prevDir != backingDir {
			_ = os.RemoveAll(prevDir)
		}
	}
	_ = os.WriteFile(prevBacking, []byte(backingDir), 0o644)
	vm.Handle = newInst
	vm.PID = newInst.PID()
	vm.State = vmm.StateRunning
	m.Store.Add(vm)
}

func (m *Manager) rollback(vm *vmm.VM, paused bool) {
	if paused && vm.Handle != nil {
		if err := vm.Handle.Resume(); err != nil {
			vm.State = vmm.StateError
			vm.LastError = "rollback resume failed: " + err.Error()
			m.Store.Add(vm)
			return
		}
	}
	vm.State = vmm.StateRunning
	m.Store.Add(vm)
}

func (m *Manager) markDead(vm *vmm.VM, err error) {
	vm.State = vmm.StateError
	vm.LastError = "instance unreachable: " + err.Error()
	m.Store.Add(vm)
}

func (m *Manager) DestroyVM(vm *vmm.VM) {
	if vm.Handle != nil {
		_ = vm.Handle.Kill()
	}
	vm.State = vmm.StateStopped
	m.Store.Delete(vm.ID)
	_ = os.RemoveAll(vm.Dir)
}
