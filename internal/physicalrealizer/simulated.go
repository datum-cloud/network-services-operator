// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

const maxVLAN = 4094

// ErrSwitchUnreachable is what a simulated switch told to fail returns.
var ErrSwitchUnreachable = errors.New("simulated switch is unreachable")

// Change is one state a simulated port reached.
type Change struct {
	Port    Port
	Carried []Membership
}

// SimulatedSwitch is an in-memory Driver. Its hooks make a change fail, apply
// only partly, or make reads fail, and it records every state a port reaches.
type SimulatedSwitch struct {
	mu             sync.Mutex
	ports          map[Port][]Membership
	history        []Change
	failedApplies  int
	partialApplies int
	failReads      bool
	beforeApply    func(Port, []Membership)
}

var _ Driver = (*SimulatedSwitch)(nil)

func NewSimulatedSwitch() *SimulatedSwitch {
	return &SimulatedSwitch{ports: map[Port][]Membership{}}
}

// FailApplies makes the next n changes fail without touching the port.
func (s *SimulatedSwitch) FailApplies(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failedApplies = n
}

// PartlyApply makes the next n changes take networks off the port and add
// none, while reporting success.
func (s *SimulatedSwitch) PartlyApply(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partialApplies = n
}

// FailReads makes every read fail until it is called with false.
func (s *SimulatedSwitch) FailReads(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failReads = fail
}

// BeforeApply runs hook at the start of every change, before the port moves.
func (s *SimulatedSwitch) BeforeApply(hook func(Port, []Membership)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeApply = hook
}

// Carried reports what port carries, without the read hooks.
func (s *SimulatedSwitch) Carried(port Port) []Membership {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ports[port])
}

// History returns every state any port reached, in order.
func (s *SimulatedSwitch) History() []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	history := make([]Change, 0, len(s.history))
	for _, change := range s.history {
		history = append(history, Change{Port: change.Port, Carried: slices.Clone(change.Carried)})
	}
	return history
}

func (s *SimulatedSwitch) Apply(_ context.Context, port Port, desired []Membership) error {
	s.mu.Lock()
	hook := s.beforeApply
	s.mu.Unlock()
	if hook != nil {
		hook(port, slices.Clone(desired))
	}

	if err := validMemberships(desired); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failedApplies > 0 {
		s.failedApplies--
		return fmt.Errorf("changing %s %s: %w", port.Switch, port.Name, ErrSwitchUnreachable)
	}
	carried := sortedMemberships(desired)
	if s.partialApplies > 0 {
		s.partialApplies--
		current := s.ports[port]
		carried = slices.DeleteFunc(carried, func(m Membership) bool { return !slices.Contains(current, m) })
	}
	if len(carried) == 0 {
		delete(s.ports, port)
	} else {
		s.ports[port] = carried
	}
	s.history = append(s.history, Change{Port: port, Carried: slices.Clone(carried)})
	return nil
}

func (s *SimulatedSwitch) Read(_ context.Context, port Port) ([]Membership, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReads {
		return nil, fmt.Errorf("reading %s %s: %w", port.Switch, port.Name, ErrSwitchUnreachable)
	}
	return slices.Clone(s.ports[port]), nil
}

func validMemberships(memberships []Membership) error {
	untagged := 0
	vlans := map[int32]bool{}
	networks := map[string]bool{}
	for _, m := range memberships {
		if networks[m.Network.String()] {
			return fmt.Errorf("network %s is listed twice", m.Network)
		}
		networks[m.Network.String()] = true
		switch {
		case m.VLAN == 0:
			untagged++
		case m.VLAN < 0 || m.VLAN > maxVLAN:
			return fmt.Errorf("VLAN %d is out of range", m.VLAN)
		case vlans[m.VLAN]:
			return fmt.Errorf("VLAN %d is listed twice", m.VLAN)
		}
		vlans[m.VLAN] = true
	}
	if untagged > 1 {
		return fmt.Errorf("a port carries at most one untagged network, got %d", untagged)
	}
	return nil
}
