package livemigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/migrate"
)

// GuestSpec describes the microVM to migrate.
type GuestSpec struct {
	KernelPath     string
	RootfsPath     string
	BootArgs       string
	Vcpus          int64
	MemMiB         int64
	RootfsReadOnly bool // shared read-only rootfs; mutable state lives in RAM and migrates with it
	Net            *NetIface
}

// NetIface is the guest network interface on the source host.
type NetIface struct {
	IfaceID  string
	GuestMAC string
	HostTap  string
}

// NetOverride remaps a restored guest NIC onto a destination-host tap so the
// guest keeps its MAC/IP after the move.
type NetOverride struct {
	IfaceID string
	HostTap string
}

// Config parameterizes a single-host live migration between two Firecracker
// processes that share a tmpfs directory (SharedDir).
type Config struct {
	FirecrackerBin string
	UffdHandlerBin string
	SharedDir      string // tmpfs visible to both source and destination
	WorkDir        string // per-run scratch for sockets and logs
	Guest          GuestSpec
	PrecopyRounds  int           // diff-snapshot passes that refresh the base memfile while the guest runs
	DestNet        []NetOverride // remap guest NICs onto the destination host's taps
	MemBackend     string        // "File" (mmap shared memfile) or "Uffd" (userspace handler)

	// Attach mode: the source and destination Firecracker processes are owned by
	// separate container "hosts" and expose their API sockets (and serial logs)
	// on the shared tmpfs. When set, the orchestrator drives these instead of
	// spawning its own, so the two VMs genuinely run on two concurrent hosts.
	Attach  bool
	SrcSock string
	DstSock string
	SrcLog  string
	DstLog  string

	FaultInjectRestore bool // force the destination restore to fail, to exercise rollback
}

// Result reports the timings of a completed migration. CutoverStartUnixNs and
// CutoverEndUnixNs bracket the blackout in wall-clock time so an external client
// (the beacon collector) can attribute its observed service gap to the cutover.
type Result struct {
	BlackoutMs         float64            `json:"blackout_ms"`
	PhasesMs           map[string]float64 `json:"phases_ms"`
	PrecopyMs          float64            `json:"precopy_ms"`
	CutoverStartUnixNs int64              `json:"cutover_start_unix_ns"`
	CutoverEndUnixNs   int64              `json:"cutover_end_unix_ns"`
	SrcConsole         string             `json:"-"`
	DstConsole         string             `json:"-"`
}

type paths struct {
	baseState, baseMem, diffState, diffMem string
	srcSock, dstSock, uffdSock             string
	srcLog, dstLog, uffdLog                string
}

func (c *Config) paths() paths {
	s, w := c.SharedDir, c.WorkDir
	return paths{
		baseState: filepath.Join(s, "base.vmstate"),
		baseMem:   filepath.Join(s, "mem"),
		diffState: filepath.Join(s, "diff.vmstate"),
		diffMem:   filepath.Join(s, "diff.mem"),
		srcSock:   filepath.Join(w, "src.sock"),
		dstSock:   filepath.Join(w, "dst.sock"),
		uffdSock:  filepath.Join(s, "uffd.sock"),
		srcLog:    filepath.Join(w, "src.log"),
		dstLog:    filepath.Join(w, "dst.log"),
		uffdLog:   filepath.Join(w, "uffd.log"),
	}
}

// Run boots a source guest, pre-copies its memory, then performs the post-copy
// cutover and returns the measured guest blackout. The source and destination
// Firecracker processes are left running (destination holds the migrated guest);
// the caller owns teardown via the returned Handles.
func Run(c Config) (*Result, *Handles, error) {
	if err := os.MkdirAll(c.SharedDir, 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(c.WorkDir, 0o755); err != nil {
		return nil, nil, err
	}
	p := c.paths()
	if c.Attach {
		p.srcSock, p.dstSock, p.srcLog, p.dstLog = c.SrcSock, c.DstSock, c.SrcLog, c.DstLog
	}

	var src *fcProcess
	var err error
	if c.Attach {
		src, err = attachFirecracker(p.srcSock)
	} else {
		src, err = spawnFirecracker(c.FirecrackerBin, p.srcSock, p.srcLog)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("source firecracker: %w", err)
	}
	h := &Handles{Src: src}
	if err := src.boot(c.Guest); err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("boot guest: %w", err)
	}
	time.Sleep(4 * time.Second) // let the guest reach its workload

	res := &Result{PhasesMs: map[string]float64{}}

	// Pre-copy: capture a base memory image while the guest runs, refreshing it
	// with successive diff snapshots so the final cutover diff is small.
	precopyStart := time.Now()
	if err := src.pause(); err != nil {
		h.Close()
		return nil, nil, err
	}
	fullStart := time.Now()
	if err := src.snapshot("Full", p.baseState, p.baseMem); err != nil {
		h.Close()
		return nil, nil, err
	}
	res.PhasesMs["base_full_snapshot"] = ms(time.Since(fullStart))
	if err := src.resume(); err != nil {
		h.Close()
		return nil, nil, err
	}
	for i := 0; i < c.PrecopyRounds; i++ {
		time.Sleep(300 * time.Millisecond)
		if err := src.pause(); err != nil {
			h.Close()
			return nil, nil, err
		}
		if err := src.snapshot("Diff", p.diffState, p.diffMem); err != nil {
			h.Close()
			return nil, nil, err
		}
		if err := migrate.MergeDiffOntoBase(p.baseMem, p.diffMem); err != nil {
			h.Close()
			return nil, nil, err
		}
		if err := src.resume(); err != nil {
			h.Close()
			return nil, nil, err
		}
	}
	res.PrecopyMs = ms(time.Since(precopyStart))

	// The destination Firecracker is idle and API-ready BEFORE the cutover (its
	// container started it, or we spawn it here) so the blackout excludes startup.
	var dst *fcProcess
	if c.Attach {
		dst, err = attachFirecracker(p.dstSock)
	} else {
		dst, err = spawnFirecracker(c.FirecrackerBin, p.dstSock, p.dstLog)
	}
	if err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("dest firecracker: %w", err)
	}
	h.Dst = dst

	// ---- cutover: the guest is not executing anywhere for this window ----
	t0 := time.Now()
	res.CutoverStartUnixNs = t0.UnixNano()

	tp := time.Now()
	if err := src.pause(); err != nil {
		h.Close()
		return nil, nil, err
	}
	res.PhasesMs["pause"] = ms(time.Since(tp))

	// From here the source is paused. Any failure rolls back by resuming it, so
	// the guest keeps running on the source rather than being lost.
	rollback := func(err error) (*Result, *Handles, error) {
		if rerr := src.resume(); rerr != nil {
			h.Close()
			return nil, nil, fmt.Errorf("%w (and rollback resume failed: %v)", err, rerr)
		}
		if h.Dst != nil {
			h.Dst.kill()
			h.Dst = nil
		}
		if h.Uffd != nil {
			h.Uffd.kill()
			h.Uffd = nil
		}
		return nil, h, fmt.Errorf("migration rolled back, guest still on source: %w", err)
	}

	tp = time.Now()
	if err := src.snapshot("Diff", p.diffState, p.diffMem); err != nil {
		return rollback(err)
	}
	res.PhasesMs["diff_snapshot"] = ms(time.Since(tp))

	tp = time.Now()
	if err := migrate.MergeDiffOntoBase(p.baseMem, p.diffMem); err != nil {
		return rollback(err)
	}
	res.PhasesMs["merge"] = ms(time.Since(tp))

	backendType := c.MemBackend
	if backendType == "" {
		backendType = "File"
	}
	backendPath := p.baseMem
	if backendType == "Uffd" {
		tp = time.Now()
		uffd, err := startUffdHandler(c.UffdHandlerBin, p.uffdSock, p.baseMem, p.uffdLog)
		if err != nil {
			return rollback(err)
		}
		h.Uffd = uffd
		res.PhasesMs["uffd_start"] = ms(time.Since(tp))
		backendPath = p.uffdSock
	}

	restoreState := p.diffState
	if c.FaultInjectRestore {
		restoreState = p.diffState + ".nonexistent"
	}
	tp = time.Now()
	if err := dst.loadAndResume(restoreState, backendType, backendPath, c.DestNet); err != nil {
		return rollback(fmt.Errorf("dest restore: %w", err))
	}
	res.PhasesMs["load_resume"] = ms(time.Since(tp))

	res.BlackoutMs = ms(time.Since(t0))
	res.CutoverEndUnixNs = time.Now().UnixNano()
	// ---- guest is now executing on the destination ----

	res.SrcConsole = p.srcLog
	res.DstConsole = p.dstLog
	return res, h, nil
}

// Handles owns the processes a migration leaves running.
type Handles struct {
	Src  *fcProcess
	Dst  *fcProcess
	Uffd *uffdHandler
}

func (h *Handles) Close() {
	if h == nil {
		return
	}
	if h.Src != nil {
		h.Src.kill()
	}
	if h.Dst != nil {
		h.Dst.kill()
	}
	if h.Uffd != nil {
		h.Uffd.kill()
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
