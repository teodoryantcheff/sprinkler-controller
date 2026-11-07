package hw

import (
	"fmt"
	"testing"
)

type fakePins struct {
	level map[int]int
	// latched snapshots of shifted data
	shift   []int
	latched []int
	trace   []string
}

func (f *fakePins) set(pin, v int) error {
	prev := f.level[pin]
	f.level[pin] = v
	f.trace = append(f.trace, fmt.Sprintf("%d=%d", pin, v))
	const latch, data, clock = 22, 27, 4
	if pin == clock && prev == 0 && v == 1 {
		f.shift = append([]int{f.level[data]}, f.shift...) // Q0 receives newest bit
	}
	if pin == latch && prev == 0 && v == 1 {
		f.latched = append([]int(nil), f.shift...)
	}
	return nil
}

func TestShiftRegBitOrderAndOE(t *testing.T) {
	fp := &fakePins{level: map[int]int{17: 1}}
	s := &ShiftReg{cfg: ShiftRegConfig{Count: 8, Latch: 22, Data: 27, Clock: 4, OE: 17}, p: fp}
	states := []bool{true, false, true, false, false, false, false, true} // zones 1,3,8
	if err := s.Set(states); err != nil {
		t.Fatal(err)
	}
	// After 8 clocks the register holds Q0..Q7; Q0 must be zone 1.
	got := fp.latched[:8]
	want := []int{1, 0, 1, 0, 0, 0, 0, 1}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("latched %v want %v", got, want)
		}
	}
	if fp.level[17] != 0 {
		t.Error("OE must go low after the first complete write")
	}
	// OE must still have been high when the latch went high the first time.
	for _, e := range fp.trace {
		if e == "17=0" {
			break
		}
		if e == "22=1" {
			goto ok
		}
	}
	t.Error("OE went low before the first latch")
ok:
	if err := s.AllOff(); err != nil {
		t.Fatal(err)
	}
	for _, v := range fp.latched[:8] {
		if v != 0 {
			t.Fatalf("AllOff left %v", fp.latched[:8])
		}
	}
}

func TestSim(t *testing.T) {
	s := NewSim(3, nil)
	if err := s.Set([]bool{true, false, true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set([]bool{true}); err == nil {
		t.Error("wrong length must fail")
	}
	s.AllOff()
	for _, v := range s.States() {
		if v {
			t.Fatal("not all off")
		}
	}
}
