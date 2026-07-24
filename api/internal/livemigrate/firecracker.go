// Package livemigrate implements post-copy live migration of a Firecracker
// microVM with a guest blackout (the interval the guest is not executing on any
// host) of tens of milliseconds. Guest memory is served to the destination over
// userfaultfd, so it is faulted in lazily after the guest resumes rather than
// copied during the blackout window.
package livemigrate

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

// fcProcess is a single Firecracker process addressed over its unix API socket.
type fcProcess struct {
	cmd    *exec.Cmd
	sock   string
	client *http.Client
	logF   *os.File
}

func spawnFirecracker(binPath, sock, logPath string) (*fcProcess, error) {
	_ = os.Remove(sock)
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "--api-sock", sock)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		logF.Close()
		return nil, fmt.Errorf("spawn firecracker: %w", err)
	}
	fc := &fcProcess{cmd: cmd, sock: sock, logF: logF, client: unixClient(sock)}
	if err := fc.waitReady(5 * time.Second); err != nil {
		fc.kill()
		return nil, err
	}
	return fc, nil
}

// attachFirecracker connects to an already-running Firecracker whose API socket
// lives on shared storage (a container on another host owns the process). The
// returned fcProcess drives it over the socket but does not own its lifecycle.
func attachFirecracker(sock string) (*fcProcess, error) {
	fc := &fcProcess{sock: sock, client: unixClient(sock)}
	if err := fc.waitReady(15 * time.Second); err != nil {
		return nil, err
	}
	return fc, nil
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

func (p *fcProcess) waitReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(p.sock); err == nil {
			if err := p.call("GET", "/version", nil); err == nil {
				return nil
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return fmt.Errorf("firecracker API socket %s not ready", p.sock)
}

func (p *fcProcess) call(method, path string, body any) error {
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
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("firecracker %s %s: %d %s", method, path, resp.StatusCode, string(data))
	}
	return nil
}

func (p *fcProcess) boot(spec GuestSpec) error {
	steps := []struct {
		path string
		body any
	}{
		{"/boot-source", map[string]any{"kernel_image_path": spec.KernelPath, "boot_args": spec.BootArgs}},
		{"/drives/rootfs", map[string]any{"drive_id": "rootfs", "path_on_host": spec.RootfsPath, "is_root_device": true, "is_read_only": spec.RootfsReadOnly}},
		{"/machine-config", map[string]any{"vcpu_count": spec.Vcpus, "mem_size_mib": spec.MemMiB, "track_dirty_pages": true}},
	}
	for _, s := range steps {
		if err := p.call("PUT", s.path, s.body); err != nil {
			return err
		}
	}
	if spec.Net != nil {
		if err := p.call("PUT", "/network-interfaces/"+spec.Net.IfaceID, map[string]any{
			"iface_id":      spec.Net.IfaceID,
			"guest_mac":     spec.Net.GuestMAC,
			"host_dev_name": spec.Net.HostTap,
		}); err != nil {
			return err
		}
	}
	return p.call("PUT", "/actions", map[string]any{"action_type": "InstanceStart"})
}

func (p *fcProcess) pause() error  { return p.call("PATCH", "/vm", map[string]any{"state": "Paused"}) }
func (p *fcProcess) resume() error { return p.call("PATCH", "/vm", map[string]any{"state": "Resumed"}) }

func (p *fcProcess) snapshot(typ, statePath, memPath string) error {
	return p.call("PUT", "/snapshot/create", map[string]any{
		"snapshot_type": typ,
		"snapshot_path": statePath,
		"mem_file_path": memPath,
	})
}

// loadAndResume restores from the vmstate and resumes the vCPUs. Guest memory is
// not read here; it faults in lazily after resume, which keeps the blackout small.
// backendType is "File" (mmap the shared memory file; the kernel page-faults it
// in) or "Uffd" (a userspace handler serves faults, e.g. over a network). File is
// preferred when source and destination share storage: no userspace round-trip
// per fault. netOverrides remaps the restored guest NICs onto this host's taps.
func (p *fcProcess) loadAndResume(statePath, backendType, backendPath string, netOverrides []NetOverride) error {
	body := map[string]any{
		"snapshot_path": statePath,
		"mem_backend":   map[string]any{"backend_type": backendType, "backend_path": backendPath},
		"resume_vm":     true,
	}
	if len(netOverrides) > 0 {
		ovr := make([]map[string]any, 0, len(netOverrides))
		for _, o := range netOverrides {
			ovr = append(ovr, map[string]any{"iface_id": o.IfaceID, "host_dev_name": o.HostTap})
		}
		body["network_overrides"] = ovr
	}
	return p.call("PUT", "/snapshot/load", body)
}

func (p *fcProcess) kill() {
	if p.logF != nil {
		p.logF.Close()
	}
	if p.cmd == nil { // attached, not owned
		return
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
	_ = os.Remove(p.sock)
}

// uffdHandler is the external page-fault handler process that serves guest
// memory to the destination Firecracker from the memory file.
type uffdHandler struct {
	cmd  *exec.Cmd
	sock string
}

func startUffdHandler(handlerBin, sock, memPath, logPath string) (*uffdHandler, error) {
	_ = os.Remove(sock)
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(handlerBin, sock, memPath)
	cmd.Stdout = logF
	cmd.Stderr = logF
	if err := cmd.Start(); err != nil {
		logF.Close()
		return nil, fmt.Errorf("spawn uffd handler: %w", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			return &uffdHandler{cmd: cmd, sock: sock}, nil
		}
		time.Sleep(time.Millisecond)
	}
	cmd.Process.Kill()
	return nil, fmt.Errorf("uffd handler socket %s not ready", sock)
}

func (h *uffdHandler) kill() {
	if h.cmd.Process != nil {
		_ = h.cmd.Process.Kill()
		_, _ = h.cmd.Process.Wait()
	}
}

func consolePath(dir string) string { return filepath.Join(dir, "console.log") }
