package vmm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type MockBackend struct {
	Defaults VMSpec
	Latency  time.Duration
}

func (b *MockBackend) Name() string        { return "mock" }
func (b *MockBackend) DefaultSpec() VMSpec { return b.Defaults }

type mockInstance struct {
	mu       sync.Mutex
	paused   bool
	memBytes int64
	dir      string
	latency  time.Duration
	pid      int
}

func (b *MockBackend) Boot(vm *VM) (Instance, error) {
	if err := writeMockMem(filepath.Join(vm.Dir, "mock.mem"), vm.Spec.MemMiB); err != nil {
		return nil, err
	}
	return &mockInstance{memBytes: vm.Spec.MemMiB << 20, dir: vm.Dir, latency: b.Latency, pid: os.Getpid()}, nil
}

func (b *MockBackend) Restore(vm *VM, paths SnapshotPaths, resume bool) (Instance, error) {
	st, err := os.Stat(paths.MemPath)
	if err != nil {
		return nil, fmt.Errorf("mock restore: %w", err)
	}
	if _, err := os.Stat(paths.StatePath); err != nil {
		return nil, fmt.Errorf("mock restore: %w", err)
	}
	inst := &mockInstance{memBytes: st.Size(), dir: vm.Dir, latency: b.Latency, pid: os.Getpid(), paused: !resume}
	return inst, nil
}

func writeMockMem(path string, memMiB int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	chunk := make([]byte, 1<<20)
	_, _ = rand.Read(chunk[:4096])
	for i := int64(0); i < memMiB; i++ {
		if _, err := f.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

func (i *mockInstance) Pause() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.paused = true
	time.Sleep(i.latency)
	return nil
}

func (i *mockInstance) Resume() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.paused = false
	time.Sleep(i.latency)
	return nil
}

func (i *mockInstance) Snapshot(typ SnapshotType, paths SnapshotPaths) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.paused {
		return fmt.Errorf("mock snapshot requires paused instance")
	}
	src := filepath.Join(i.dir, "mock.mem")
	if typ == SnapDiff {
		if err := os.WriteFile(paths.MemPath, []byte("mockdiff"), 0o644); err != nil {
			return err
		}
	} else {
		if err := copyFile(src, paths.MemPath); err != nil {
			return err
		}
	}
	token := make([]byte, 8)
	_, _ = rand.Read(token)
	return os.WriteFile(paths.StatePath, []byte("mockvmstate-"+hex.EncodeToString(token)), 0o644)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := out.ReadFrom(in); err != nil {
		return err
	}
	return out.Sync()
}

func (i *mockInstance) Kill() error { return nil }
func (i *mockInstance) PID() int    { return i.pid }
