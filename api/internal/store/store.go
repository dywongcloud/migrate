package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"

	"github.com/dylanwongtencent/daedal/api/internal/vmm"
)

type Store struct {
	mu  sync.RWMutex
	vms map[string]*vmm.VM
}

func New() *Store {
	return &Store{vms: map[string]*vmm.VM{}}
}

func NewID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func (s *Store) Add(vm *vmm.VM) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vms[vm.ID] = vm
}

func (s *Store) Get(id string) (*vmm.VM, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vm, ok := s.vms[id]
	if !ok {
		return nil, fmt.Errorf("vm %q not found", id)
	}
	return vm, nil
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.vms, id)
}

func (s *Store) List() []*vmm.VM {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*vmm.VM, 0, len(s.vms))
	for _, vm := range s.vms {
		out = append(out, vm)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (s *Store) CompareAndSetState(id string, from, to vmm.VMState) (*vmm.VM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	vm, ok := s.vms[id]
	if !ok {
		return nil, fmt.Errorf("vm %q not found", id)
	}
	if vm.State != from {
		return nil, fmt.Errorf("vm %q is %s, expected %s", id, vm.State, from)
	}
	vm.State = to
	return vm, nil
}
