package vmm

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

type VMState string

const (
	StateCreating  VMState = "creating"
	StateRunning   VMState = "running"
	StatePaused    VMState = "paused"
	StateMigrating VMState = "migrating"
	StateStopped   VMState = "stopped"
	StateError     VMState = "error"
)

type VMSpec struct {
	Name            string `json:"name"`
	Backend         string `json:"backend"`
	Vcpus           int64  `json:"vcpus"`
	MemMiB          int64  `json:"mem_mib"`
	KernelPath      string `json:"kernel_path,omitempty"`
	RootfsPath      string `json:"rootfs_path,omitempty"`
	BootArgs        string `json:"boot_args,omitempty"`
	TrackDirtyPages bool   `json:"track_dirty_pages"`
}

type VM struct {
	ID        string    `json:"id"`
	Spec      VMSpec    `json:"spec"`
	State     VMState   `json:"state"`
	Dir       string    `json:"dir"`
	PID       int       `json:"pid,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	LastError string    `json:"last_error,omitempty"`
	Handle    Instance  `json:"-"`
}

type SnapshotType string

const (
	SnapFull SnapshotType = "Full"
	SnapDiff SnapshotType = "Diff"
)

type SnapshotPaths struct {
	StatePath string
	MemPath   string
}

type Instance interface {
	Pause() error
	Resume() error
	Snapshot(typ SnapshotType, paths SnapshotPaths) error
	Kill() error
	PID() int
}

type Backend interface {
	Name() string
	Boot(vm *VM) (Instance, error)
	Restore(vm *VM, paths SnapshotPaths, resume bool) (Instance, error)
	DefaultSpec() VMSpec
}

type Capabilities struct {
	Arch        string   `json:"arch"`
	DevKVM      bool     `json:"dev_kvm"`
	KVMPVM      bool     `json:"kvm_pvm_module"`
	Backends    []string `json:"backends"`
	DefaultName string   `json:"default_backend"`
}

func DetectCapabilities(configured []string) Capabilities {
	caps := Capabilities{Arch: hostArch()}
	if _, err := os.Stat("/dev/kvm"); err == nil {
		caps.DevKVM = true
	}
	if data, err := os.ReadFile("/proc/modules"); err == nil {
		if strings.Contains(string(data), "kvm_pvm") {
			caps.KVMPVM = true
		}
	}
	caps.Backends = configured
	switch {
	case caps.KVMPVM:
		caps.DefaultName = "pvm"
	case caps.DevKVM:
		caps.DefaultName = "kvm"
	default:
		caps.DefaultName = "mock"
	}
	for _, b := range configured {
		if b == caps.DefaultName {
			return caps
		}
	}
	if len(configured) > 0 {
		caps.DefaultName = configured[0]
	}
	return caps
}

func hostArch() string {
	return runtime.GOARCH
}

func ValidateSpec(s VMSpec) error {
	if s.Vcpus < 1 || s.Vcpus > 32 {
		return fmt.Errorf("vcpus must be 1..32, got %d", s.Vcpus)
	}
	if s.MemMiB < 32 || s.MemMiB > 32768 {
		return fmt.Errorf("mem_mib must be 32..32768, got %d", s.MemMiB)
	}
	return nil
}
