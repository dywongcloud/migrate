package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dylanwongtencent/daedal/api/internal/livemigrate"
)

// liveMigrateMain runs one post-copy live migration between two Firecracker
// processes on this host and prints the measured guest blackout. It is the
// single-host core that the two-container demo and the p99 harness both drive.
func liveMigrateMain(args []string) {
	fs := flag.NewFlagSet("livemigrate", flag.ExitOnError)
	fcBin := fs.String("firecracker", "/usr/local/bin/firecracker", "firecracker binary")
	uffdBin := fs.String("uffd-handler", "", "uffd_on_demand_handler binary")
	kernel := fs.String("kernel", "", "guest kernel path")
	rootfs := fs.String("rootfs", "", "guest rootfs path")
	shared := fs.String("shared-dir", "/dev/shm/daedal-lm", "shared tmpfs dir")
	workDir := fs.String("work-dir", "/tmp/daedal-lm", "scratch dir")
	mem := fs.Int64("mem", 128, "guest RAM MiB")
	rounds := fs.Int("precopy-rounds", 3, "pre-copy diff refresh rounds")
	target := fs.Float64("target-ms", 30, "blackout SLA in ms")
	jsonOut := fs.String("report", "", "write JSON result to this path")
	ifaceID := fs.String("net-iface", "", "guest NIC id (enables networked migration)")
	guestMAC := fs.String("guest-mac", "06:00:AC:14:00:02", "guest MAC (kept across hosts)")
	srcTap := fs.String("src-tap", "tap-src", "source host tap device")
	dstTap := fs.String("dst-tap", "tap-dst", "destination host tap device")
	memBackend := fs.String("mem-backend", "File", "destination memory backend: File (shared storage) or Uffd (network)")
	rootfsRO := fs.Bool("rootfs-ro", true, "open the guest rootfs read-only (shared across hosts)")
	failRestore := fs.Bool("fault-restore", false, "force the destination restore to fail, to exercise rollback")
	attach := fs.Bool("attach", false, "drive Firecracker processes owned by separate container hosts")
	srcSock := fs.String("src-sock", "", "source Firecracker API socket (attach mode)")
	dstSock := fs.String("dst-sock", "", "destination Firecracker API socket (attach mode)")
	srcLog := fs.String("src-log", "", "source Firecracker serial log (attach mode)")
	dstLog := fs.String("dst-log", "", "destination Firecracker serial log (attach mode)")
	_ = fs.Parse(args)

	guest := livemigrate.GuestSpec{
		KernelPath:     *kernel,
		RootfsPath:     *rootfs,
		BootArgs:       "console=ttyS0 reboot=k panic=1 pci=off init=/init",
		Vcpus:          1,
		MemMiB:         *mem,
		RootfsReadOnly: *rootfsRO,
	}
	var destNet []livemigrate.NetOverride
	if *ifaceID != "" {
		guest.Net = &livemigrate.NetIface{IfaceID: *ifaceID, GuestMAC: *guestMAC, HostTap: *srcTap}
		destNet = []livemigrate.NetOverride{{IfaceID: *ifaceID, HostTap: *dstTap}}
	}

	cfg := livemigrate.Config{
		FirecrackerBin:     *fcBin,
		UffdHandlerBin:     *uffdBin,
		SharedDir:          *shared,
		WorkDir:            *workDir,
		PrecopyRounds:      *rounds,
		Guest:              guest,
		DestNet:            destNet,
		MemBackend:         *memBackend,
		Attach:             *attach,
		SrcSock:            *srcSock,
		DstSock:            *dstSock,
		SrcLog:             *srcLog,
		DstLog:             *dstLog,
		FaultInjectRestore: *failRestore,
	}

	res, handles, err := livemigrate.Run(cfg)
	if err != nil {
		// A rollback leaves the guest running on the source: verify it continues.
		if handles != nil && strings.Contains(err.Error(), "rolled back") {
			defer handles.Close()
			srcLogPath := cfg.SrcLog
			if !cfg.Attach {
				srcLogPath = filepath.Join(cfg.WorkDir, "src.log")
			}
			before := lastHeartbeat(srcLogPath)
			time.Sleep(3 * time.Second)
			after := lastHeartbeat(srcLogPath)
			fmt.Printf("ROLLBACK ok: %v\n", err)
			fmt.Printf("source guest still alive: heartbeat %d -> %d (advanced=%v)\n", before, after, after > before)
			if after <= before {
				os.Exit(1)
			}
			return
		}
		log.Fatalf("live migration failed: %v", err)
	}
	defer handles.Close()

	// Confirm the guest continued on the destination rather than rebooting: its
	// heartbeat counter must advance past the last value seen on the source.
	time.Sleep(4 * time.Second)
	srcLast := lastHeartbeat(res.SrcConsole)
	dstLast := lastHeartbeat(res.DstConsole)
	dstReboots := countMatches(res.DstConsole, "GUEST_BOOTED")
	continued := dstLast > srcLast && dstReboots == 0

	phaseParts := make([]string, 0, len(res.PhasesMs))
	for _, k := range []string{"pause", "diff_snapshot", "merge", "uffd_start", "load_resume"} {
		phaseParts = append(phaseParts, fmt.Sprintf("%s=%.1f", k, res.PhasesMs[k]))
	}

	fmt.Printf("blackout=%.1fms target=%.0fms pass=%v | precopy=%.0fms | %s\n",
		res.BlackoutMs, *target, res.BlackoutMs <= *target && continued, res.PrecopyMs, strings.Join(phaseParts, " "))
	fmt.Printf("continuity: src_last_heartbeat=%d dst_last_heartbeat=%d dst_reboots=%d continued=%v\n",
		srcLast, dstLast, dstReboots, continued)

	if *jsonOut != "" {
		out := map[string]any{
			"blackout_ms":           res.BlackoutMs,
			"target_ms":             *target,
			"precopy_ms":            res.PrecopyMs,
			"phases_ms":             res.PhasesMs,
			"mem_mib":               *mem,
			"src_last_hb":           srcLast,
			"dst_last_hb":           dstLast,
			"dst_reboots":           dstReboots,
			"continued":             continued,
			"cutover_start_unix_ns": res.CutoverStartUnixNs,
			"cutover_end_unix_ns":   res.CutoverEndUnixNs,
			"pass":                  res.BlackoutMs <= *target && continued,
		}
		buf, _ := json.MarshalIndent(out, "", "  ")
		_ = os.WriteFile(*jsonOut, buf, 0o644)
	}

	if res.BlackoutMs > *target || !continued {
		os.Exit(1)
	}
}

func lastHeartbeat(logPath string) int {
	out, err := exec.Command("sh", "-c", "grep -a HEARTBEAT "+logPath+" | tail -1 | grep -oE '[0-9]+' | tail -1").Output()
	if err != nil {
		return -1
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}

func countMatches(logPath, pat string) int {
	out, err := exec.Command("sh", "-c", "grep -ac "+pat+" "+logPath).Output()
	if err != nil {
		return 0
	}
	var n int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n)
	return n
}
