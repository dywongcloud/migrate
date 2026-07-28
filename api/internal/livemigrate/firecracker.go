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
	"syscall"
	"time"
)

type fcProcess struct {
	cmd     *exec.Cmd
	sock    string
	client  *http.Client
	logF    *os.File
	consolF *os.File
}

func openConsoleFifo(path string) (*os.File, error) {
	_ = os.Remove(path)
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		return nil, fmt.Errorf("mkfifo %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

func spawnFirecracker(binPath, sock, logPath, consoleFifo string) (*fcProcess, error) {
	_ = os.Remove(sock)
	logF, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath, "--api-sock", sock)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var consolF *os.File
	if consoleFifo != "" {
		consolF, err = openConsoleFifo(consoleFifo)
		if err != nil {
			logF.Close()
			return nil, err
		}
		cmd.Stdin = consolF
	}
	if err := cmd.Start(); err != nil {
		logF.Close()
		if consolF != nil {
			consolF.Close()
		}
		return nil, fmt.Errorf("spawn firecracker: %w", err)
	}
	fc := &fcProcess{cmd: cmd, sock: sock, logF: logF, consolF: consolF, client: unixClient(sock)}
	if err := fc.waitReady(5 * time.Second); err != nil {
		fc.kill()
		return nil, err
	}
	return fc, nil
}

func attachFirecracker(sock string) (*fcProcess, error) {
	fc := &fcProcess{sock: sock, client: unixClient(sock)}
	if err := fc.waitReady(15 * time.Second); err != nil {
		return nil, err
	}
	return fc, nil
}

func firecrackerAlive(sock string) bool {
	if _, err := os.Stat(sock); err != nil {
		return false
	}
	probe := &fcProcess{sock: sock, client: unixClient(sock)}
	probe.client.Timeout = 2 * time.Second
	return probe.call("GET", "/version", nil) == nil
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
	_, err := p.callJSON(method, path, body)
	return err
}

func (p *fcProcess) callJSON(method, path string, body any) ([]byte, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, "http://fc"+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("firecracker %s %s: %d %s", method, path, resp.StatusCode, string(data))
	}
	return data, nil
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

func (p *fcProcess) machineConfig() ([]byte, error) {
	return p.callJSON("GET", "/machine-config", nil)
}

func (p *fcProcess) snapshot(typ, statePath, memPath string) error {
	return p.call("PUT", "/snapshot/create", map[string]any{
		"snapshot_type": typ,
		"snapshot_path": statePath,
		"mem_file_path": memPath,
	})
}

func (p *fcProcess) loadAndResume(statePath, backendType, backendPath string, netOverrides []NetOverride, trackDirtyPages bool) error {
	body := map[string]any{
		"snapshot_path": statePath,
		"mem_backend":   map[string]any{"backend_type": backendType, "backend_path": backendPath},
		"resume_vm":     true,
	}
	if trackDirtyPages {
		body["enable_diff_snapshots"] = true
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
	if p.consolF != nil {
		p.consolF.Close()
	}
	if p.cmd == nil {
		return
	}
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
		_, _ = p.cmd.Process.Wait()
	}
	_ = os.Remove(p.sock)
}

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
