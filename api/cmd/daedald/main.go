package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/metrics"
	"github.com/dylanwongtencent/daedal/api/internal/migrate"
	"github.com/dylanwongtencent/daedal/api/internal/server"
	"github.com/dylanwongtencent/daedal/api/internal/store"
	"github.com/dylanwongtencent/daedal/api/internal/vmm"
)

type backendConfig struct {
	KernelPath      string `json:"kernel_path"`
	RootfsPath      string `json:"rootfs_path"`
	BootArgs        string `json:"boot_args"`
	Vcpus           int64  `json:"vcpus"`
	MemMiB          int64  `json:"mem_mib"`
	TrackDirtyPages *bool  `json:"track_dirty_pages"`
}

type daemonConfig struct {
	Listen         string                   `json:"listen"`
	StateDir       string                   `json:"state_dir"`
	FirecrackerBin string                   `json:"firecracker_bin"`
	Backends       map[string]backendConfig `json:"backends"`
}

func defaultConfig() daemonConfig {
	return daemonConfig{
		Listen:         "127.0.0.1:7031",
		StateDir:       "/var/lib/daedald",
		FirecrackerBin: "/usr/local/bin/firecracker",
		Backends: map[string]backendConfig{
			"mock": {Vcpus: 2, MemMiB: 128},
		},
	}
}

func loadConfig(path string) (daemonConfig, error) {
	cfg := defaultConfig()
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func buildBackends(cfg daemonConfig) map[string]vmm.Backend {
	out := map[string]vmm.Backend{}
	for name, bc := range cfg.Backends {
		track := true
		if bc.TrackDirtyPages != nil {
			track = *bc.TrackDirtyPages
		}
		spec := vmm.VMSpec{
			Backend:         name,
			Vcpus:           max64(bc.Vcpus, 1),
			MemMiB:          max64(bc.MemMiB, 128),
			KernelPath:      bc.KernelPath,
			RootfsPath:      bc.RootfsPath,
			BootArgs:        bc.BootArgs,
			TrackDirtyPages: track,
		}
		if name == "mock" {
			out[name] = &vmm.MockBackend{Defaults: spec, Latency: 5 * time.Millisecond}
			continue
		}
		if spec.BootArgs == "" {
			spec.BootArgs = "console=ttyS0 reboot=k panic=1"
		}
		out[name] = &vmm.FirecrackerBackend{
			BackendName: name,
			BinPath:     cfg.FirecrackerBin,
			Defaults:    spec,
		}
	}
	return out
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) > 1 && os.Args[1] == "bench" {
		benchMain(os.Args[2:])
		return
	}
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "", "path to daemon config JSON")
	listenOverride := fs.String("listen", "", "override listen address (host:port or unix:/path)")
	_ = fs.Parse(args)

	cfg, err := loadConfig(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *listenOverride != "" {
		cfg.Listen = *listenOverride
	}
	if err := os.MkdirAll(cfg.StateDir, 0o755); err != nil {
		log.Fatalf("state dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.StateDir, "vms"), 0o755); err != nil {
		log.Fatalf("state dir: %v", err)
	}

	st := store.New()
	rec := metrics.NewRecorder(cfg.StateDir)
	backends := buildBackends(cfg)
	names := make([]string, 0, len(backends))
	for n := range backends {
		names = append(names, n)
	}
	sort.Strings(names)
	caps := vmm.DetectCapabilities(names)
	mgr := migrate.NewManager(st, backends, rec, cfg.StateDir)
	srv := &server.Server{
		Store:    st,
		Manager:  mgr,
		Recorder: rec,
		Caps:     caps,
		StateDir: cfg.StateDir,
		Started:  time.Now(),
	}

	var ln net.Listener
	if strings.HasPrefix(cfg.Listen, "unix:") {
		sockPath := strings.TrimPrefix(cfg.Listen, "unix:")
		_ = os.Remove(sockPath)
		ln, err = net.Listen("unix", sockPath)
	} else {
		ln, err = net.Listen("tcp", cfg.Listen)
	}
	if err != nil {
		log.Fatalf("listen %s: %v", cfg.Listen, err)
	}
	log.Printf("daedald listening on %s | arch=%s backends=%v default=%s kvm=%v pvm=%v",
		cfg.Listen, caps.Arch, caps.Backends, caps.DefaultName, caps.DevKVM, caps.KVMPVM)
	log.Fatal(http.Serve(ln, srv.Routes()))
}

type benchReport struct {
	Backend     string              `json:"backend"`
	Mode        string              `json:"mode"`
	MemMiB      int64               `json:"mem_mib"`
	N           int                 `json:"n"`
	OK          int                 `json:"ok"`
	Failed      int                 `json:"failed"`
	TotalMs     metrics.Percentiles `json:"total_ms"`
	DowntimeMs  metrics.Percentiles `json:"downtime_ms"`
	P99TargetMs float64             `json:"p99_target_ms"`
	Pass        bool                `json:"pass"`
	Runs        []benchRun          `json:"runs"`
	StartedAt   time.Time           `json:"started_at"`
	FinishedAt  time.Time           `json:"finished_at"`
}

type benchRun struct {
	I          int     `json:"i"`
	TotalMs    float64 `json:"total_ms"`
	DowntimeMs float64 `json:"downtime_ms"`
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
}

func benchMain(args []string) {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	api := fs.String("api", "http://127.0.0.1:7031", "daemon base URL")
	n := fs.Int("n", 100, "number of migrations")
	mode := fs.String("mode", "precopy", "migration mode: cold|precopy")
	mem := fs.Int64("mem", 0, "guest mem MiB (0 = backend default)")
	backend := fs.String("backend", "auto", "backend: auto|kvm|pvm|mock")
	name := fs.String("name", "bench", "vm name")
	reportPath := fs.String("report", "", "write JSON report to this path")
	target := fs.Float64("target-ms", 20000, "p99 assertion target in ms")
	keepVM := fs.Bool("keep-vm", false, "leave the bench VM running")
	_ = fs.Parse(args)

	client := &http.Client{Timeout: 15 * time.Minute}
	post := func(path string, body any, out any) error {
		buf, _ := json.Marshal(body)
		resp, err := client.Post(*api+path, "application/json", bytes.NewReader(buf))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode >= 300 {
			return fmt.Errorf("POST %s: %d %s", path, resp.StatusCode, string(data))
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}

	createBody := map[string]any{"name": *name, "backend": *backend}
	if *mem > 0 {
		createBody["mem_mib"] = *mem
	}
	var vm struct {
		ID   string `json:"id"`
		Spec struct {
			MemMiB  int64  `json:"mem_mib"`
			Backend string `json:"backend"`
		} `json:"spec"`
	}
	if err := post("/v1/vms", createBody, &vm); err != nil {
		log.Fatalf("create vm: %v", err)
	}
	log.Printf("bench vm %s backend=%s mem=%d, running %d %s migrations",
		vm.ID, vm.Spec.Backend, vm.Spec.MemMiB, *n, *mode)

	report := benchReport{
		Backend:     vm.Spec.Backend,
		Mode:        *mode,
		MemMiB:      vm.Spec.MemMiB,
		N:           *n,
		P99TargetMs: *target,
		StartedAt:   time.Now(),
	}
	var totals, downtimes []float64
	for i := 0; i < *n; i++ {
		var mig struct {
			TotalMs    float64 `json:"total_ms"`
			DowntimeMs float64 `json:"downtime_ms"`
			Status     string  `json:"status"`
			Error      string  `json:"error"`
		}
		err := post("/v1/vms/"+vm.ID+"/migrate", map[string]any{"target": "local", "mode": *mode}, &mig)
		run := benchRun{I: i, TotalMs: mig.TotalMs, DowntimeMs: mig.DowntimeMs, OK: err == nil && mig.Status == "succeeded"}
		if err != nil {
			run.Error = err.Error()
		} else if mig.Error != "" {
			run.Error = mig.Error
		}
		report.Runs = append(report.Runs, run)
		if run.OK {
			report.OK++
			totals = append(totals, mig.TotalMs)
			downtimes = append(downtimes, mig.DowntimeMs)
		} else {
			report.Failed++
			log.Printf("run %d FAILED: %s", i, run.Error)
		}
		if (i+1)%10 == 0 {
			log.Printf("progress %d/%d", i+1, *n)
		}
	}
	report.FinishedAt = time.Now()
	report.TotalMs = metrics.ComputePercentiles(totals)
	report.DowntimeMs = metrics.ComputePercentiles(downtimes)
	report.Pass = report.Failed == 0 && report.TotalMs.P99 < *target

	if !*keepVM {
		req, _ := http.NewRequest("DELETE", *api+"/v1/vms/"+vm.ID, nil)
		if resp, err := client.Do(req); err == nil {
			resp.Body.Close()
		}
	}
	out, _ := json.MarshalIndent(report, "", " ")
	if *reportPath != "" {
		_ = os.MkdirAll(filepath.Dir(*reportPath), 0o755)
		if err := os.WriteFile(*reportPath, out, 0o644); err != nil {
			log.Fatalf("write report: %v", err)
		}
	}
	fmt.Printf("bench backend=%s mode=%s mem=%dMiB n=%d ok=%d failed=%d p50=%.0fms p95=%.0fms p99=%.0fms downtime_p99=%.0fms pass=%v\n",
		report.Backend, report.Mode, report.MemMiB, report.N, report.OK, report.Failed,
		report.TotalMs.P50, report.TotalMs.P95, report.TotalMs.P99, report.DowntimeMs.P99, report.Pass)
	if !report.Pass {
		os.Exit(1)
	}
}
