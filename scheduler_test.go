package ecf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustScheduler(t testing.TB, w *World, step time.Duration) *Scheduler {
	t.Helper()
	s, err := NewScheduler(w, step)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustAdd(t testing.TB, s *Scheduler, name string, system SystemFunc, options ...SystemOption) {
	t.Helper()
	if err := s.Add(name, system, options...); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulerOrderingIntervalsAndCommands(t *testing.T) {
	w := NewWorld()
	s := mustScheduler(t, w, 10*time.Millisecond)
	var order []string
	mustAdd(t, s, "spawn", func(f *Frame) error {
		if f.Delta != 10*time.Millisecond || f.Time != time.Duration(f.Tick)*f.Delta {
			t.Fatal("incorrect frame timing")
		}
		order = append(order, fmt.Sprintf("spawn:%d", f.Tick))
		f.Commands.Defer(func(w *World) error { return Set(w, w.NewEntity(), active{}) })
		return nil
	})
	q := Query1[active](w)
	mustAdd(t, s, "observe", func(f *Frame) error {
		count := 0
		q.Each(func(Entity, *active) bool { count++; return true })
		if count != int(f.Tick) {
			t.Fatal("previous system commands were not flushed")
		}
		order = append(order, fmt.Sprintf("observe:%d", f.Tick))
		return nil
	})
	mustAdd(t, s, "slow", func(f *Frame) error {
		if f.Delta != 30*time.Millisecond || f.Time != 30*time.Millisecond {
			t.Fatal("wrong slow-system delta")
		}
		order = append(order, fmt.Sprintf("slow:%d", f.Tick))
		return nil
	}, Every(3))
	for range 4 {
		if err := s.Step(); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"spawn:1", "observe:1", "spawn:2", "observe:2", "spawn:3", "observe:3", "slow:3", "spawn:4", "observe:4"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v", order)
	}
	if s.Tick() != 4 || s.Time() != 40*time.Millisecond || s.Err() != nil {
		t.Fatal("wrong scheduler counters")
	}
	if err := s.Add("late", SystemFunc(func(*Frame) error { return nil })); !errors.Is(err, ErrScheduleStarted) {
		t.Fatal(err)
	}
}

func TestSlowEventSubscribersReceiveEveryEvent(t *testing.T) {
	w := NewWorld()
	s := mustScheduler(t, w, time.Second/60)
	fast, slow := mustSubscribe[uint64](t, w, 6), mustSubscribe[uint64](t, w, 6)
	var fastEvents, slowEvents []uint64
	mustAdd(t, s, "publish", func(f *Frame) error { return Publish(w, f.Tick) })
	mustAdd(t, s, "fast", func(*Frame) error { fastEvents = append(fastEvents, drain(fast)...); return nil })
	mustAdd(t, s, "slow", func(f *Frame) error {
		if f.Delta != 6*(time.Second/60) {
			t.Fatal("wrong interval duration")
		}
		slowEvents = append(slowEvents, drain(slow)...)
		return nil
	}, Every(6))
	for range 12 {
		if err := s.Step(); err != nil {
			t.Fatal(err)
		}
	}
	if len(slowEvents) != 12 || !reflect.DeepEqual(fastEvents, slowEvents) {
		t.Fatal("slow reader lost events")
	}
}

func TestSchedulerValidation(t *testing.T) {
	w := NewWorld()
	for _, step := range []time.Duration{0, -1} {
		if _, err := NewScheduler(w, step); !errors.Is(err, ErrInvalidStep) {
			t.Fatal(err)
		}
	}
	if _, err := NewScheduler(nil, time.Second); err == nil {
		t.Fatal("accepted nil world")
	}
	s := mustScheduler(t, w, time.Second)
	noop := SystemFunc(func(*Frame) error { return nil })
	for _, tc := range []struct {
		name    string
		system  System
		options []SystemOption
	}{
		{"", noop, nil}, {"nil", nil, nil}, {"nilfunc", SystemFunc(nil), nil},
		{"zero", noop, []SystemOption{Every(0)}}, {"overflow", noop, []SystemOption{Every(math.MaxUint64)}},
		{"niloption", noop, []SystemOption{nil}},
	} {
		if err := s.Add(tc.name, tc.system, tc.options...); err == nil {
			t.Fatalf("accepted invalid system %q", tc.name)
		}
	}
	if err := s.Add("ok", noop); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("ok", noop); err == nil {
		t.Fatal("accepted duplicate system name")
	}
	long := mustScheduler(t, NewWorld(), time.Duration(math.MaxInt64))
	if err := long.Step(); err != nil {
		t.Fatal(err)
	}
	if err := long.Step(); !errors.Is(err, ErrSimulationTimeOverflow) {
		t.Fatal(err)
	}
	if long.Tick() != 1 {
		t.Fatal("overflow advanced tick")
	}
}

func TestSchedulerFailurePreservesAppliedWork(t *testing.T) {
	w := NewWorld()
	s := mustScheduler(t, w, time.Second)
	want := errors.New("game failure")
	mustAdd(t, s, "before", func(f *Frame) error { f.World.NewEntity(); return nil })
	mustAdd(t, s, "broken", func(f *Frame) error {
		SetResource(w, 42)
		f.Commands.Defer(func(w *World) error { w.NewEntity(); return nil })
		return want
	})
	mustAdd(t, s, "after", func(*Frame) error { t.Fatal("ran after failure"); return nil })
	err := s.Step()
	if !errors.Is(err, want) || !strings.Contains(err.Error(), `"broken" at tick 1`) {
		t.Fatal(err)
	}
	if w.Len() != 1 || s.commands.Len() != 0 {
		t.Fatal("failure did not discard pending commands")
	}
	if value, _ := GetResource[int](w); *value != 42 {
		t.Fatal("direct change was unexpectedly rolled back")
	}
	if s.Step() != err || s.Tick() != 1 {
		t.Fatal("failed scheduler resumed")
	}
}

func TestSchedulerCommandFailureAndPanic(t *testing.T) {
	for _, panicInstead := range []bool{false, true} {
		t.Run(fmt.Sprint(panicInstead), func(t *testing.T) {
			w := NewWorld()
			s := mustScheduler(t, w, time.Second)
			want := errors.New("command failure")
			mustAdd(t, s, "commands", func(f *Frame) error {
				f.Commands.Defer(func(w *World) error { w.NewEntity(); return nil })
				f.Commands.Defer(func(*World) error {
					if panicInstead {
						panic(want)
					}
					return want
				})
				f.Commands.Defer(func(w *World) error { w.NewEntity(); return nil })
				return nil
			})
			if panicInstead {
				assertPanic(t, want, func() { _ = s.Step() })
				if !errors.Is(s.Err(), ErrStepPanicked) {
					t.Fatal(s.Err())
				}
			} else if err := s.Step(); !errors.Is(err, want) {
				t.Fatal(err)
			}
			if w.Len() != 1 || s.commands.Len() != 0 || s.stepping {
				t.Fatal("command failure corrupted scheduler state")
			}
		})
	}
}

func TestSchedulerCancellationAndReentry(t *testing.T) {
	w := NewWorld()
	s := mustScheduler(t, w, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.StepContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Tick() != 0 || s.Err() != nil || s.started {
		t.Fatal("pre-step cancellation changed scheduler")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	mustAdd(t, s, "cancel", func(f *Frame) error {
		if err := s.Step(); !errors.Is(err, ErrReentrantStep) {
			t.Fatal(err)
		}
		if f.Context != ctx {
			t.Fatal("context not delivered")
		}
		cancel()
		return nil
	})
	mustAdd(t, s, "later", func(*Frame) error { t.Fatal("ran after cancellation"); return nil })
	if err := s.StepContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Err() == nil || s.Tick() != 1 {
		t.Fatal("partial canceled tick was not faulted")
	}
}

func TestMovementAcrossTickRates(t *testing.T) {
	for _, hz := range []int{10, 20, 30, 50, 60, 120} {
		t.Run(fmt.Sprint(hz), func(t *testing.T) {
			w := NewWorld()
			e := w.NewEntity()
			mustSet(t, w, e, position{})
			mustSet(t, w, e, velocity{X: 3})
			q := Query2[position, velocity](w)
			s := mustScheduler(t, w, time.Second/time.Duration(hz))
			mustAdd(t, s, "movement", func(f *Frame) error {
				q.Each(func(_ Entity, p *position, v *velocity) bool { p.X += v.X * f.Delta.Seconds(); return true })
				return nil
			})
			for range hz {
				if err := s.Step(); err != nil {
					t.Fatal(err)
				}
			}
			p, _ := Get[position](w, e)
			if math.Abs(p.X-3) > 1e-6 {
				t.Fatalf("position after one nominal second = %g", p.X)
			}
		})
	}
}

func TestIndependentWorldsConcurrently(t *testing.T) {
	const worlds = 12
	var wg sync.WaitGroup
	results := make(chan error, worlds)
	for world := range worlds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := NewWorld()
			e := w.NewEntity()
			if err := Set(w, e, position{X: float64(world)}); err != nil {
				results <- err
				return
			}
			s, err := NewScheduler(w, time.Millisecond)
			if err != nil {
				results <- err
				return
			}
			q := Query1[position](w)
			err = s.Add("advance", SystemFunc(func(*Frame) error {
				q.Each(func(_ Entity, p *position) bool { p.X++; return true })
				return nil
			}))
			if err != nil {
				results <- err
				return
			}
			for range 100 {
				if err := s.Step(); err != nil {
					results <- err
					return
				}
			}
			p, _ := Get[position](w, e)
			if p.X != float64(world+100) {
				results <- fmt.Errorf("world %d: position %g", world, p.X)
			}
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		t.Error(err)
	}
}
