package vmm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type FirecrackerBackend struct {
	BackendName string
	BinPath     string
	Defaults    VMSpec
}

func (b *FirecrackerBackend) Name() string        { return b.BackendName }
func (b *FirecrackerBackend) DefaultSpec() VMSpec { return b.Defaults }

type fcInstance struct {
	cmd     *exec.Cmd
	sock    string
	client  *http.Client
	console *os.File
}

func (b *FirecrackerBackend) spawn(vm *VM) (*fcInstance, error) {
	sock := filepath.Join(vm.Dir, "fc.sock")
	_ = os.Remove(sock)
	consolePath := filepath.Join(vm.Dir, "console.log")
	console, err := os.OpenFile(consolePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(b.BinPath, "--api-sock", sock)
	cmd.Stdout = console
	cmd.Stderr = console
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		console.Close()
		return nil, fmt.Errorf("spawn firecracker: %w", err)
	}
	inst := &fcInstance{cmd: cmd, sock: sock, console: console, client: unixClient(sock)}
	if err := inst.waitAPI(5 * time.Second); err != nil {
		inst.Kill()
		return nil, err
	}
	return inst, nil
}

func unixClient(sock string) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
}

func (i *fcInstance) waitAPI(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(i.sock); err == nil {
			if err := i.call("GET", "/version", nil, nil); err == nil {
				return nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("firecracker API socket not ready after %s", timeout)
}

type fcError struct {
	FaultMessage string `json:"fault_message"`
}

func (i *fcInstance) call(method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, "http://fc"+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := i.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		var fe fcError
		_ = json.Unmarshal(data, &fe)
		if fe.FaultMessage == "" {
			fe.FaultMessage = string(data)
		}
		return fmt.Errorf("firecracker %s %s: %d %s", method, path, resp.StatusCode, fe.FaultMessage)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (b *FirecrackerBackend) Boot(vm *VM) (Instance, error) {
	inst, err := b.spawn(vm)
	if err != nil {
		return nil, err
	}
	spec := vm.Spec
	steps := []struct {
		method, path string
		body         any
	}{
		{"PUT", "/boot-source", map[string]any{
			"kernel_image_path": spec.KernelPath,
			"boot_args":         spec.BootArgs,
		}},
		{"PUT", "/drives/rootfs", map[string]any{
			"drive_id":       "rootfs",
			"path_on_host":   spec.RootfsPath,
			"is_root_device": true,
			"is_read_only":   false,
		}},
		{"PUT", "/machine-config", map[string]any{
			"vcpu_count":        spec.Vcpus,
			"mem_size_mib":      spec.MemMiB,
			"track_dirty_pages": spec.TrackDirtyPages,
		}},
		{"PUT", "/actions", map[string]any{"action_type": "InstanceStart"}},
	}
	for _, s := range steps {
		if err := inst.call(s.method, s.path, s.body, nil); err != nil {
			inst.Kill()
			return nil, err
		}
	}
	return inst, nil
}

func (b *FirecrackerBackend) Restore(vm *VM, paths SnapshotPaths, resume bool) (Instance, error) {
	inst, err := b.spawn(vm)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"snapshot_path": paths.StatePath,
		"mem_backend": map[string]any{
			"backend_type": "File",
			"backend_path": paths.MemPath,
		},
		"enable_diff_snapshots": vm.Spec.TrackDirtyPages,
		"resume_vm":             resume,
	}
	if err := inst.call("PUT", "/snapshot/load", body, nil); err != nil {
		inst.Kill()
		return nil, err
	}
	return inst, nil
}

func (i *fcInstance) Pause() error {
	return i.call("PATCH", "/vm", map[string]any{"state": "Paused"}, nil)
}

func (i *fcInstance) Resume() error {
	return i.call("PATCH", "/vm", map[string]any{"state": "Resumed"}, nil)
}

func (i *fcInstance) Snapshot(typ SnapshotType, paths SnapshotPaths) error {
	return i.call("PUT", "/snapshot/create", map[string]any{
		"snapshot_type": string(typ),
		"snapshot_path": paths.StatePath,
		"mem_file_path": paths.MemPath,
	}, nil)
}

func (i *fcInstance) Kill() error {
	if i.console != nil {
		defer i.console.Close()
	}
	if i.cmd.Process == nil {
		return nil
	}
	_ = i.cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _, _ = i.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
	}
	_ = os.Remove(i.sock)
	return nil
}

func (i *fcInstance) PID() int {
	if i.cmd.Process == nil {
		return 0
	}
	return i.cmd.Process.Pid
}
