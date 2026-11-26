// SPDX-License-Identifier: AGPL-3.0-only

package physicalrealizer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

const maxVLAN = 4094

var ErrSwitchUnreachable = errors.New("simulated switch is unreachable")

type Change struct {
	Port    Port
	Carried []Membership
}

// SimulatedSwitch is an in-memory Driver that records every state a port
// reaches, with hooks to fail or partly apply changes and to fail reads.
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

func (s *SimulatedSwitch) FailReads(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failReads = fail
}

func (s *SimulatedSwitch) BeforeApply(hook func(Port, []Membership)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeApply = hook
}

func (s *SimulatedSwitch) Carried(port Port) []Membership {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ports[port])
}

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

func (s *SimulatedSwitch) Ports(_ context.Context) ([]Port, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failReads {
		return nil, fmt.Errorf("listing ports: %w", ErrSwitchUnreachable)
	}
	ports := make([]Port, 0, len(s.ports))
	for port := range s.ports {
		ports = append(ports, port)
	}
	slices.SortFunc(ports, func(a, b Port) int {
		return strings.Compare(a.Switch+"\x00"+a.Name, b.Switch+"\x00"+b.Name)
	})
	return ports, nil
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
