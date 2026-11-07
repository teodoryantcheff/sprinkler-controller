// Package hw holds the output drivers (valves) and sensor inputs.
//
// Only internal/runner may call an Outputs value. Drivers start with all
// outputs off and must leave them off when closed.
package hw

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Outputs drives the valve outputs.
type Outputs interface {
	// Count is the number of outputs.
	Count() int
	// Set writes every output at once; len(states) == Count().
	Set(states []bool) error
	// AllOff switches every output off.
	AllOff() error
	// Close switches everything off and releases the hardware.
	Close() error
	// Name identifies the driver ("sim", "shiftreg", "gpiorelay").
	Name() string
}

// Input is a digital sensor input. Active reports whether the sensor is
// tripped (wet), after normally-open / normally-closed handling.
type Input interface {
	Active() (bool, error)
	Close() error
}

// Sim is a simulated output driver. It never touches hardware.
type Sim struct {
	mu     sync.Mutex
	states []bool
	log    *slog.Logger
	// Fail makes the next writes fail (for tests).
	Fail int
}

// NewSim returns a simulated driver with n outputs.
func NewSim(n int, log *slog.Logger) *Sim {
	if log == nil {
		log = slog.Default()
	}
	return &Sim{states: make([]bool, n), log: log}
}

func (s *Sim) Name() string { return "sim" }
func (s *Sim) Count() int   { return len(s.states) }

func (s *Sim) Set(states []bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(states) != len(s.states) {
		return fmt.Errorf("sim: want %d states, got %d", len(s.states), len(states))
	}
	if s.Fail > 0 {
		s.Fail--
		return fmt.Errorf("sim: injected write failure")
	}
	changed := false
	for i := range states {
		changed = changed || states[i] != s.states[i]
	}
	copy(s.states, states)
	if changed {
		s.log.Info("sim outputs", "bits", bitString(states))
	}
	return nil
}

func (s *Sim) AllOff() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.states {
		s.states[i] = false
	}
	return nil
}

func (s *Sim) Close() error { return s.AllOff() }

// States returns a copy of the current outputs.
func (s *Sim) States() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]bool(nil), s.states...)
}

func bitString(states []bool) string {
	var b strings.Builder
	for _, v := range states {
		if v {
			b.WriteByte('1')
		} else {
			b.WriteByte('0')
		}
	}
	return b.String()
}

// SimInput is a sensor that can be set from the UI in development.
type SimInput struct {
	mu  sync.Mutex
	wet bool
}

func (s *SimInput) Active() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wet, nil
}

// SetWet changes the simulated sensor state.
func (s *SimInput) SetWet(v bool) {
	s.mu.Lock()
	s.wet = v
	s.mu.Unlock()
}

func (s *SimInput) Close() error { return nil }
