package hw

import (
	"errors"
	"fmt"
	"testing"
)

const (
	pinLatch, pinData, pinClock, pinOE = 22, 27, 4, 17
)

func newFakeShiftReg(count int) (*ShiftReg, *fakePins) {
	fp := &fakePins{level: map[int]int{pinOE: 1}}
	return &ShiftReg{cfg: ShiftRegConfig{Count: count, Latch: pinLatch, Data: pinData, Clock: pinClock, OE: pinOE}, p: fp}, fp
}

// failingPins fails every write to one pin after `after` successful ones.
type failingPins struct {
	*fakePins
	pin   int
	after int
}

func (f *failingPins) set(pin, v int) error {
	if pin == f.pin {
		if f.after == 0 {
			return errors.New("gpio write failed")
		}
		f.after--
	}
	return f.fakePins.set(pin, v)
}

func TestShiftRegChained(t *testing.T) {
	s, fp := newFakeShiftReg(16) // two 74HC595 in a chain
	states := make([]bool, 16)
	states[0], states[8], states[15] = true, true, true
	if err := s.Set(states); err != nil {
		t.Fatal(err)
	}
	if len(fp.latched) != 16 {
		t.Fatalf("latched %d bits, want 16", len(fp.latched))
	}
	for i, on := range states {
		want := 0
		if on {
			want = 1
		}
		if fp.latched[i] != want {
			t.Fatalf("output %d = %d, want %d (latched %v)", i+1, fp.latched[i], want, fp.latched)
		}
	}
}

func TestShiftRegDataStableOnClockEdge(t *testing.T) {
	s, fp := newFakeShiftReg(8)
	if err := s.Set([]bool{true, true, false, true, false, false, true, false}); err != nil {
		t.Fatal(err)
	}
	// Data may only change while the clock is low, and the latch must be
	// low for the whole shift.
	clock, latch := 0, 1
	for _, e := range fp.trace {
		var pin, v int
		if _, err := fmt.Sscanf(e, "%d=%d", &pin, &v); err != nil {
			t.Fatal(err)
		}
		switch pin {
		case pinClock:
			clock = v
			if v == 1 && latch != 0 {
				t.Fatalf("clock edge with latch high: %v", fp.trace)
			}
		case pinLatch:
			latch = v
		case pinData:
			if clock != 0 {
				t.Fatalf("data changed while clock high: %v", fp.trace)
			}
		}
	}
	if latch != 1 {
		t.Fatal("latch must end high")
	}
}

func TestShiftRegWrongLengthTouchesNothing(t *testing.T) {
	s, fp := newFakeShiftReg(8)
	if err := s.Set(make([]bool, 7)); err == nil {
		t.Fatal("wrong length must fail")
	}
	if len(fp.trace) != 0 {
		t.Fatalf("pins written on a rejected Set: %v", fp.trace)
	}
	if fp.level[pinOE] != 1 {
		t.Fatal("OE must stay high")
	}
}

func TestShiftRegFailedWriteDisablesOutputs(t *testing.T) {
	fp := &fakePins{level: map[int]int{pinOE: 1}}
	// 8 clock writes per full shift: the first Set succeeds, then the
	// clock line dies part-way through the next one.
	fl := &failingPins{fakePins: fp, pin: pinClock, after: 2*8 + 3}
	s := &ShiftReg{cfg: ShiftRegConfig{Count: 8, Latch: pinLatch, Data: pinData, Clock: pinClock, OE: pinOE}, p: fl}
	if err := s.Set([]bool{true, false, false, false, false, false, false, false}); err != nil {
		t.Fatal(err)
	}
	if fp.level[pinOE] != 0 {
		t.Fatal("outputs should be enabled after a good write")
	}
	if err := s.AllOff(); err == nil {
		t.Fatal("AllOff should report the failed write")
	}
	if fp.level[pinOE] != 1 {
		t.Fatal("a failed AllOff must at least drive OE high (outputs off)")
	}
	if s.enabled {
		t.Fatal("driver must remember OE is high")
	}
	// Once the line works again, OE is lowered only after a full write.
	fl.pin = -1
	fp.trace = nil
	if err := s.AllOff(); err != nil {
		t.Fatal(err)
	}
	if fp.trace[len(fp.trace)-1] != "17=0" {
		t.Fatalf("OE must go low last, trace %v", fp.trace)
	}
}

func TestShiftRegCloseLeavesOutputsOff(t *testing.T) {
	s, fp := newFakeShiftReg(8)
	if err := s.Set([]bool{true, true, true, true, true, true, true, true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	for _, v := range fp.latched[:8] { // the fake keeps older bits past Count
		if v != 0 {
			t.Fatalf("Close left %v latched", fp.latched[:8])
		}
	}
	if fp.level[pinOE] != 1 {
		t.Fatal("Close must leave OE high")
	}
	if s.Name() != "shiftreg" || s.Count() != 8 {
		t.Fatal("name/count")
	}
}

type fakeLines struct {
	values []int
	err    error
	closed bool
}

func (f *fakeLines) SetValues(v []int) error {
	if f.err != nil {
		return f.err
	}
	f.values = append([]int(nil), v...)
	return nil
}

func (f *fakeLines) Close() error { f.closed = true; return nil }

func TestRelay(t *testing.T) {
	fl := &fakeLines{}
	r := &Relay{cfg: RelayConfig{Pins: []int{5, 6, 13}}, lines: fl}
	if r.Name() != "gpiorelay" || r.Count() != 3 {
		t.Fatal("name/count")
	}
	if err := r.Set([]bool{true, false, true}); err != nil {
		t.Fatal(err)
	}
	if fl.values[0] != 1 || fl.values[1] != 0 || fl.values[2] != 1 {
		t.Fatalf("values %v", fl.values)
	}
	if err := r.Set([]bool{true}); err == nil {
		t.Fatal("wrong length must fail")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if !fl.closed || fl.values[0]+fl.values[1]+fl.values[2] != 0 {
		t.Fatalf("Close must switch all off and release: %v closed=%v", fl.values, fl.closed)
	}
	fl.err = errors.New("ioctl failed")
	if err := r.AllOff(); err == nil {
		t.Fatal("write error must surface")
	}
}

type fakeLine struct {
	v   int
	err error
}

func (f *fakeLine) Value() (int, error) { return f.v, f.err }
func (f *fakeLine) Close() error        { return nil }

func TestGPIOInput(t *testing.T) {
	for _, c := range []struct {
		normally string
		level    int
		active   bool
	}{
		{"open", 1, false}, // pull-up, switch open: dry
		{"open", 0, true},  // switch closed by rain
		{"closed", 0, false},
		{"closed", 1, true},
	} {
		in := &GPIOInput{line: &fakeLine{v: c.level}, normally: c.normally}
		got, err := in.Active()
		if err != nil || got != c.active {
			t.Errorf("normally %s level %d: active=%v err=%v, want %v", c.normally, c.level, got, err, c.active)
		}
	}
	in := &GPIOInput{line: &fakeLine{err: errors.New("read failed")}, normally: "open"}
	if _, err := in.Active(); err == nil {
		t.Error("read error must surface")
	}
	if err := in.Close(); err != nil {
		t.Error(err)
	}
}

func TestSimInputAndSim(t *testing.T) {
	var in SimInput
	if v, _ := in.Active(); v {
		t.Fatal("sim sensor starts dry")
	}
	in.SetWet(true)
	if v, _ := in.Active(); !v {
		t.Fatal("SetWet(true) should read wet")
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	s := NewSim(2, nil)
	if s.Name() != "sim" || s.Count() != 2 {
		t.Fatal("name/count")
	}
	_ = s.Set([]bool{true, true})
	if err := s.Close(); err != nil || s.States()[0] || s.States()[1] {
		t.Fatalf("Close must switch all off: %v %v", s.States(), err)
	}
}
