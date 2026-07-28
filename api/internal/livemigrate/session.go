package livemigrate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type HostSpec struct {
	Name string
	Tap  string
}

type Session struct {
	mu         sync.Mutex
	cfg        Config
	hosts      [2]HostSpec
	current    int
	memSlot    int
	migrations int
	fc         *fcProcess
	uffd       *uffdHandler
	guestLost  bool
}

func StartSession(c Config, hosts [2]HostSpec) (*Session, error) {
	if c.Attach {
		return nil, fmt.Errorf("persistent session owns its Firecracker processes and cannot run in attach mode")
	}
	if hosts[0].Name == "" || hosts[1].Name == "" || hosts[0].Name == hosts[1].Name {
		return nil, fmt.Errorf("persistent session needs two distinct host names, got %q and %q", hosts[0].Name, hosts[1].Name)
	}
	if hosts[0].Tap == "" || hosts[1].Tap == "" {
		return nil, fmt.Errorf("persistent session needs a host tap for %s and %s", hosts[0].Name, hosts[1].Name)
	}
	if c.Guest.Net == nil || c.Guest.Net.IfaceID == "" {
		return nil, fmt.Errorf("persistent session needs Guest.Net with an iface id so the NIC can be remapped on the destination")
	}
	for _, tap := range []string{hosts[0].Tap, hosts[1].Tap} {
		if _, err := os.Stat(filepath.Join("/sys/class/net", tap)); err != nil {
			return nil, fmt.Errorf("host tap %s is not present on this host: %w", tap, err)
		}
	}
	if err := prepareDirs(&c); err != nil {
		return nil, err
	}

	s := &Session{cfg: c, hosts: hosts}
	for i := range hosts {
		if firecrackerAlive(s.sock(i)) {
			return nil, fmt.Errorf("a Firecracker is already live on %s (%s) and may still hold %s read-write; stop it before starting a persistent session",
				hosts[i].Name, s.sock(i), c.Guest.RootfsPath)
		}
	}
	fc, err := spawnFirecracker(c.FirecrackerBin, s.sock(0), s.log(0), s.console(0))
	if err != nil {
		return nil, fmt.Errorf("firecracker on %s: %w", hosts[0].Name, err)
	}
	guest := c.Guest
	nic := *c.Guest.Net
	nic.HostTap = hosts[0].Tap
	guest.Net = &nic
	if err := fc.boot(guest); err != nil {
		fc.kill()
		return nil, fmt.Errorf("boot persistent guest on %s: %w", hosts[0].Name, err)
	}
	s.fc = fc
	return s, nil
}

func (s *Session) sock(i int) string {
	return filepath.Join(s.cfg.WorkDir, s.hosts[i].Name+".sock")
}

func (s *Session) log(i int) string {
	return filepath.Join(s.cfg.WorkDir, s.hosts[i].Name+".log")
}

func (s *Session) console(i int) string {
	return filepath.Join(s.cfg.WorkDir, s.hosts[i].Name+".console")
}

func (s *Session) ConsolePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.console(s.current)
}

func (s *Session) slotPaths(slot int) paths {
	d := s.cfg.SharedDir
	return paths{
		baseState: filepath.Join(d, fmt.Sprintf("session-base-%d.vmstate", slot)),
		baseMem:   filepath.Join(d, fmt.Sprintf("session-mem-%d", slot)),
		diffState: filepath.Join(d, fmt.Sprintf("session-diff-%d.vmstate", slot)),
		diffMem:   filepath.Join(d, fmt.Sprintf("session-diff-%d.mem", slot)),
	}
}

func (s *Session) removeSlot(slot int) {
	p := s.slotPaths(slot)
	for _, f := range []string{p.baseState, p.baseMem, p.diffState, p.diffMem} {
		_ = os.Remove(f)
	}
}

func (s *Session) CurrentHost() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[s.current].Name
}

func (s *Session) NextHost() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hosts[1-s.current].Name
}

func (s *Session) GuestSock() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sock(s.current)
}

func (s *Session) GuestMachineConfig() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fc == nil {
		return nil, fmt.Errorf("persistent guest is not running")
	}
	return s.fc.machineConfig()
}

func (s *Session) Migrate(progress ProgressFunc) (*Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.guestLost || s.fc == nil {
		return nil, fmt.Errorf("persistent guest is gone, restart the server to boot a new one")
	}

	dst := 1 - s.current
	nextSlot := 1 - s.memSlot
	s.migrations++

	c := s.cfg
	c.Progress = progress
	c.DestNet = []NetOverride{{IfaceID: s.cfg.Guest.Net.IfaceID, HostTap: s.hosts[dst].Tap}}

	p := s.slotPaths(nextSlot)
	p.srcSock, p.srcLog = s.sock(s.current), s.log(s.current)
	p.dstSock, p.dstLog = s.sock(dst), s.log(dst)
	p.uffdSock = filepath.Join(c.SharedDir, fmt.Sprintf("session-uffd-%d.sock", s.migrations))
	p.uffdLog = filepath.Join(c.WorkDir, fmt.Sprintf("session-uffd-%d.log", s.migrations))

	res := &Result{PhasesMs: map[string]float64{}}
	if err := precopy(&c, p, s.fc, res); err != nil {
		s.removeSlot(nextSlot)
		return nil, fmt.Errorf("precopy on %s: %w", s.hosts[s.current].Name, err)
	}

	dstFc, err := spawnFirecracker(c.FirecrackerBin, p.dstSock, p.dstLog, s.console(dst))
	if err != nil {
		s.removeSlot(nextSlot)
		return nil, fmt.Errorf("firecracker on %s: %w", s.hosts[dst].Name, err)
	}

	uffd, rolledBack, err := cutover(&c, p, s.fc, dstFc, res, true)
	if err != nil {
		dstFc.kill()
		s.removeSlot(nextSlot)
		if !rolledBack {
			s.fc.kill()
			s.fc = nil
			s.guestLost = true
		}
		return nil, err
	}

	drained, drainedUffd, drainedSlot := s.fc, s.uffd, s.memSlot
	s.fc, s.uffd, s.current, s.memSlot = dstFc, uffd, dst, nextSlot
	drained.kill()
	if drainedUffd != nil {
		drainedUffd.kill()
	}
	s.removeSlot(drainedSlot)

	res.SrcConsole, res.DstConsole = p.srcLog, p.dstLog
	return res, nil
}

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fc != nil {
		s.fc.kill()
		s.fc = nil
	}
	if s.uffd != nil {
		s.uffd.kill()
		s.uffd = nil
	}
	s.removeSlot(0)
	s.removeSlot(1)
	s.guestLost = true
}
