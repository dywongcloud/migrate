package livemigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/migrate"
)

type GuestSpec struct {
	KernelPath     string
	RootfsPath     string
	BootArgs       string
	Vcpus          int64
	MemMiB         int64
	RootfsReadOnly bool
	Net            *NetIface
}

type NetIface struct {
	IfaceID  string
	GuestMAC string
	HostTap  string
}

type NetOverride struct {
	IfaceID string
	HostTap string
}

type EventKind string

const (
	EventCutoverStart       EventKind = "cutover_start"
	EventLoadResumeComplete EventKind = "load_resume_complete"
	EventRollback           EventKind = "rollback"
)

type Event struct {
	Kind       EventKind
	BlackoutMs float64
	Err        error
}

type ProgressFunc func(Event)

type Config struct {
	FirecrackerBin string
	UffdHandlerBin string
	SharedDir      string
	WorkDir        string
	Guest          GuestSpec
	PrecopyRounds  int
	DestNet        []NetOverride
	MemBackend     string

	Attach  bool
	SrcSock string
	DstSock string
	SrcLog  string
	DstLog  string

	FaultInjectRestore bool

	Progress ProgressFunc
}

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

func prepareDirs(c *Config) error {
	if err := os.MkdirAll(c.SharedDir, 0o755); err != nil {
		return err
	}
	return os.MkdirAll(c.WorkDir, 0o755)
}

func openFirecracker(c *Config, sock, logPath string) (*fcProcess, error) {
	if c.Attach {
		return attachFirecracker(sock)
	}
	return spawnFirecracker(c.FirecrackerBin, sock, logPath, "")
}

func precopy(c *Config, p paths, src *fcProcess, res *Result) error {
	start := time.Now()
	if err := src.pause(); err != nil {
		return err
	}
	fullStart := time.Now()
	if err := src.snapshot("Full", p.baseState, p.baseMem); err != nil {
		return err
	}
	res.PhasesMs["base_full_snapshot"] = ms(time.Since(fullStart))
	if err := src.resume(); err != nil {
		return err
	}
	for i := 0; i < c.PrecopyRounds; i++ {
		time.Sleep(300 * time.Millisecond)
		if err := src.pause(); err != nil {
			return err
		}
		if err := src.snapshot("Diff", p.diffState, p.diffMem); err != nil {
			return err
		}
		if err := migrate.MergeDiffOntoBase(p.baseMem, p.diffMem); err != nil {
			return err
		}
		if err := src.resume(); err != nil {
			return err
		}
	}
	res.PrecopyMs = ms(time.Since(start))
	return nil
}

func cutover(c *Config, p paths, src, dst *fcProcess, res *Result, trackDirtyPagesOnRestore bool) (*uffdHandler, bool, error) {
	t0 := time.Now()
	res.CutoverStartUnixNs = t0.UnixNano()
	if c.Progress != nil {
		c.Progress(Event{Kind: EventCutoverStart})
	}

	tp := time.Now()
	if err := src.pause(); err != nil {
		return nil, false, err
	}
	res.PhasesMs["pause"] = ms(time.Since(tp))

	rollback := func(cause error, uffd *uffdHandler) (*uffdHandler, bool, error) {
		if uffd != nil {
			uffd.kill()
		}
		if rerr := src.resume(); rerr != nil {
			return nil, false, fmt.Errorf("%w (and rollback resume failed: %v)", cause, rerr)
		}
		wrapped := fmt.Errorf("migration rolled back, guest still on source: %w", cause)
		if c.Progress != nil {
			c.Progress(Event{Kind: EventRollback, Err: wrapped})
		}
		return nil, true, wrapped
	}

	tp = time.Now()
	if err := src.snapshot("Diff", p.diffState, p.diffMem); err != nil {
		return rollback(err, nil)
	}
	res.PhasesMs["diff_snapshot"] = ms(time.Since(tp))

	tp = time.Now()
	if err := migrate.MergeDiffOntoBase(p.baseMem, p.diffMem); err != nil {
		return rollback(err, nil)
	}
	res.PhasesMs["merge"] = ms(time.Since(tp))

	backendType := c.MemBackend
	if backendType == "" {
		backendType = "File"
	}
	backendPath := p.baseMem
	var uffd *uffdHandler
	if backendType == "Uffd" {
		tp = time.Now()
		started, err := startUffdHandler(c.UffdHandlerBin, p.uffdSock, p.baseMem, p.uffdLog)
		if err != nil {
			return rollback(err, nil)
		}
		uffd = started
		res.PhasesMs["uffd_start"] = ms(time.Since(tp))
		backendPath = p.uffdSock
	}

	restoreState := p.diffState
	if c.FaultInjectRestore {
		restoreState = p.diffState + ".nonexistent"
	}
	tp = time.Now()
	if err := dst.loadAndResume(restoreState, backendType, backendPath, c.DestNet, trackDirtyPagesOnRestore); err != nil {
		return rollback(fmt.Errorf("dest restore: %w", err), uffd)
	}
	res.PhasesMs["load_resume"] = ms(time.Since(tp))

	res.BlackoutMs = ms(time.Since(t0))
	if c.Progress != nil {
		c.Progress(Event{Kind: EventLoadResumeComplete, BlackoutMs: res.BlackoutMs})
	}
	res.CutoverEndUnixNs = time.Now().UnixNano()
	return uffd, false, nil
}

func Run(c Config) (*Result, *Handles, error) {
	if err := prepareDirs(&c); err != nil {
		return nil, nil, err
	}
	p := c.paths()
	if c.Attach {
		p.srcSock, p.dstSock, p.srcLog, p.dstLog = c.SrcSock, c.DstSock, c.SrcLog, c.DstLog
	}

	src, err := openFirecracker(&c, p.srcSock, p.srcLog)
	if err != nil {
		return nil, nil, fmt.Errorf("source firecracker: %w", err)
	}
	h := &Handles{Src: src}
	if err := src.boot(c.Guest); err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("boot guest: %w", err)
	}
	time.Sleep(4 * time.Second)

	res := &Result{PhasesMs: map[string]float64{}}
	if err := precopy(&c, p, src, res); err != nil {
		h.Close()
		return nil, nil, err
	}

	dst, err := openFirecracker(&c, p.dstSock, p.dstLog)
	if err != nil {
		h.Close()
		return nil, nil, fmt.Errorf("dest firecracker: %w", err)
	}
	h.Dst = dst

	uffd, rolledBack, err := cutover(&c, p, src, dst, res, false)
	if err != nil {
		h.Dst.kill()
		h.Dst = nil
		if !rolledBack {
			h.Close()
			return nil, nil, err
		}
		return nil, h, err
	}
	h.Uffd = uffd

	res.SrcConsole = p.srcLog
	res.DstConsole = p.dstLog
	return res, h, nil
}

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
