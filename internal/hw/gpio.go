package hw

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

const consumer = "sprinkler"

// pins is the small surface the shift-register logic needs; it is faked
// in tests.
type pins interface {
	set(pin, v int) error
}

// ShiftRegConfig are BCM line numbers on the chip.
type ShiftRegConfig struct {
	Chip  string
	Count int
	Latch int
	Data  int
	Clock int
	OE    int // output enable, active low: high = outputs off
}

// ShiftReg drives a 74HC595 chain (OpenSprinkler Pi board).
type ShiftReg struct {
	mu      sync.Mutex
	cfg     ShiftRegConfig
	p       pins
	lines   map[int]*gpiocdev.Line
	chip    *gpiocdev.Chip
	enabled bool // OE driven low after the first good write
}

// CheckLinesFree returns an error naming the consumer if any of the lines
// is already in use (e.g. by OSPy or OpenSprinkler via sysfs).
func CheckLinesFree(chipName string, offsets []int) error {
	c, err := gpiocdev.NewChip(chipName)
	if err != nil {
		return fmt.Errorf("open %s: %w", chipName, err)
	}
	defer c.Close()
	var busy []string
	for _, o := range offsets {
		li, err := c.LineInfo(o)
		if err != nil {
			return fmt.Errorf("line %d info: %w", o, err)
		}
		if li.Used {
			who := li.Consumer
			if who == "" {
				who = "unknown (sysfs?)"
			}
			busy = append(busy, fmt.Sprintf("GPIO%d held by %s", o, who))
		}
	}
	if len(busy) > 0 {
		return fmt.Errorf("refusing to start, GPIO lines in use: %s — stop OpenSprinkler/OSPy first", strings.Join(busy, ", "))
	}
	return nil
}

// OpenShiftReg requests the lines exclusively. OE is requested high
// (outputs off) and the register is cleared before OE goes low.
func OpenShiftReg(cfg ShiftRegConfig) (*ShiftReg, error) {
	if cfg.Count <= 0 {
		return nil, errors.New("shiftreg: count must be > 0")
	}
	offs := []int{cfg.OE, cfg.Latch, cfg.Data, cfg.Clock}
	if err := CheckLinesFree(cfg.Chip, offs); err != nil {
		return nil, err
	}
	c, err := gpiocdev.NewChip(cfg.Chip, gpiocdev.WithConsumer(consumer))
	if err != nil {
		return nil, err
	}
	s := &ShiftReg{cfg: cfg, chip: c, lines: map[int]*gpiocdev.Line{}}
	// OE first, high: outputs disabled whatever the register holds.
	for _, o := range offs {
		initial := 0
		if o == cfg.OE {
			initial = 1
		}
		l, err := c.RequestLine(o, gpiocdev.AsOutput(initial))
		if err != nil {
			s.release()
			if errors.Is(err, syscall.EBUSY) {
				return nil, fmt.Errorf("GPIO%d is busy (another controller running?): %w", o, err)
			}
			return nil, fmt.Errorf("request GPIO%d: %w", o, err)
		}
		s.lines[o] = l
	}
	s.p = cdevPins(s.lines)
	if err := s.write(make([]bool, cfg.Count)); err != nil {
		s.release()
		return nil, err
	}
	return s, nil
}

type cdevPins map[int]*gpiocdev.Line

func (c cdevPins) set(pin, v int) error { return c[pin].SetValue(v) }

func (s *ShiftReg) Name() string { return "shiftreg" }
func (s *ShiftReg) Count() int   { return s.cfg.Count }

// write clocks out all bits, highest output first, then latches. OE is
// pulled low only after the first complete write.
func (s *ShiftReg) write(states []bool) error {
	c := s.cfg
	if err := s.p.set(c.Latch, 0); err != nil {
		return err
	}
	for i := len(states) - 1; i >= 0; i-- {
		v := 0
		if states[i] {
			v = 1
		}
		if err := s.p.set(c.Clock, 0); err != nil {
			return err
		}
		if err := s.p.set(c.Data, v); err != nil {
			return err
		}
		if err := s.p.set(c.Clock, 1); err != nil {
			return err
		}
	}
	if err := s.p.set(c.Latch, 1); err != nil {
		return err
	}
	if !s.enabled {
		if err := s.p.set(c.OE, 0); err != nil {
			return err
		}
		s.enabled = true
	}
	return nil
}

func (s *ShiftReg) Set(states []bool) error {
	if len(states) != s.cfg.Count {
		return fmt.Errorf("shiftreg: want %d states, got %d", s.cfg.Count, len(states))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(states)
}

func (s *ShiftReg) AllOff() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := s.write(make([]bool, s.cfg.Count))
	if err != nil && s.p != nil {
		// Could not clear the register: at least disable the outputs.
		_ = s.p.set(s.cfg.OE, 1)
		s.enabled = false
	}
	return err
}

func (s *ShiftReg) Close() error {
	err := s.AllOff()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p != nil {
		_ = s.p.set(s.cfg.OE, 1)
	}
	s.release()
	return err
}

func (s *ShiftReg) release() {
	for _, l := range s.lines {
		l.Close()
	}
	s.lines = nil
	if s.chip != nil {
		s.chip.Close()
		s.chip = nil
	}
}

// RelayConfig drives one GPIO line per zone.
type RelayConfig struct {
	Chip      string
	Pins      []int
	ActiveLow bool
}

// Relay is a GPIO relay board driver.
type Relay struct {
	mu    sync.Mutex
	cfg   RelayConfig
	lines *gpiocdev.Lines
	chip  *gpiocdev.Chip
}

// OpenRelay requests all relay lines, initially off.
func OpenRelay(cfg RelayConfig) (*Relay, error) {
	if len(cfg.Pins) == 0 {
		return nil, errors.New("gpiorelay: no pins configured")
	}
	if err := CheckLinesFree(cfg.Chip, cfg.Pins); err != nil {
		return nil, err
	}
	c, err := gpiocdev.NewChip(cfg.Chip, gpiocdev.WithConsumer(consumer))
	if err != nil {
		return nil, err
	}
	opts := []gpiocdev.LineReqOption{gpiocdev.AsOutput(make([]int, len(cfg.Pins))...)}
	if cfg.ActiveLow {
		opts = append(opts, gpiocdev.AsActiveLow)
	}
	ls, err := c.RequestLines(cfg.Pins, opts...)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("request relay lines: %w", err)
	}
	return &Relay{cfg: cfg, lines: ls, chip: c}, nil
}

func (r *Relay) Name() string { return "gpiorelay" }
func (r *Relay) Count() int   { return len(r.cfg.Pins) }

func (r *Relay) Set(states []bool) error {
	if len(states) != len(r.cfg.Pins) {
		return fmt.Errorf("gpiorelay: want %d states, got %d", len(r.cfg.Pins), len(states))
	}
	vals := make([]int, len(states))
	for i, s := range states {
		if s {
			vals[i] = 1
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lines.SetValues(vals)
}

func (r *Relay) AllOff() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lines.SetValues(make([]int, len(r.cfg.Pins)))
}

func (r *Relay) Close() error {
	err := r.AllOff()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines.Close()
	r.chip.Close()
	return err
}

// GPIOInput is a sensor on a GPIO line with the internal pull-up enabled.
type GPIOInput struct {
	line     *gpiocdev.Line
	normally string // open: pull-up reads 1 when dry; closed: reads 0 when dry
}

// OpenInput requests a sensor line as input with pull-up and a hardware
// debounce where the kernel supports it.
func OpenInput(chip string, pin int, normally string) (*GPIOInput, error) {
	if err := CheckLinesFree(chip, []int{pin}); err != nil {
		return nil, err
	}
	l, err := gpiocdev.RequestLine(chip, pin, gpiocdev.AsInput, gpiocdev.WithPullUp,
		gpiocdev.WithDebounce(50*time.Millisecond), gpiocdev.WithConsumer(consumer))
	if err != nil {
		// Older kernels (Pi 1 on 5.10) may lack debounce support.
		l, err = gpiocdev.RequestLine(chip, pin, gpiocdev.AsInput, gpiocdev.WithPullUp, gpiocdev.WithConsumer(consumer))
		if err != nil {
			return nil, fmt.Errorf("request sensor GPIO%d: %w", pin, err)
		}
	}
	return &GPIOInput{line: l, normally: normally}, nil
}

func (g *GPIOInput) Active() (bool, error) {
	v, err := g.line.Value()
	if err != nil {
		return false, err
	}
	// Same rule as OpenSprinkler: tripped when the level differs from the
	// idle level (1 for normally open with pull-up, 0 for normally closed).
	idle := 1
	if g.normally == "closed" {
		idle = 0
	}
	return v != idle, nil
}

func (g *GPIOInput) Close() error { return g.line.Close() }
