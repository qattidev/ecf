package tick

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/qattidev/ecf"
)

type fakeClock struct {
	now       time.Time
	waits     []time.Duration
	oversleep time.Duration
	closed    bool
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.waits = append(c.waits, duration)
	c.now = c.now.Add(duration)
	if len(c.waits) == 1 {
		c.now = c.now.Add(c.oversleep)
	}
	return nil
}
func (c *fakeClock) Close() { c.closed = true }

func fixture(t *testing.T, config Config) (*ecf.Scheduler, *Runner, *fakeClock) {
	t.Helper()
	s, err := ecf.NewScheduler(ecf.NewWorld(), 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	r, err := New(s, config)
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeClock{now: time.Unix(0, 0)}
	r.clock = c
	return s, r, c
}

func TestFixedStepsAndFirstDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var batches []Stats
	s, r, c := fixture(t, Config{OnBatch: func(stats Stats) {
		batches = append(batches, stats)
		if len(batches) == 3 {
			cancel()
		}
	}})
	var times []time.Duration
	if err := s.Add("observe", ecf.SystemFunc(func(f *ecf.Frame) error {
		if f.Delta != 10*time.Millisecond {
			t.Fatalf("Delta = %v", f.Delta)
		}
		times = append(times, c.Now().Sub(time.Unix(0, 0)))
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond}
	if !reflect.DeepEqual(times, want) {
		t.Fatalf("execution times = %v", times)
	}
	for _, stats := range batches {
		if stats.Steps != 1 || stats.Dropped != 0 || stats.Duration != 0 {
			t.Fatalf("batch = %+v", stats)
		}
	}
	if !c.closed || s.Tick() != 3 || s.Time() != 30*time.Millisecond {
		t.Fatal("incorrect shutdown or counters")
	}
}

func TestBoundedCatchUpAndFractionalRemainder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		limit     int
		oversleep time.Duration
		steps     int
		dropped   time.Duration
	}{
		{"default", 0, 95 * time.Millisecond, 5, 50 * time.Millisecond},
		{"custom", 2, 35 * time.Millisecond, 2, 20 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var batches []Stats
			s, r, c := fixture(t, Config{MaxCatchUpSteps: tc.limit, OnBatch: func(stats Stats) {
				batches = append(batches, stats)
				if len(batches) == 2 {
					cancel()
				}
			}})
			c.oversleep = tc.oversleep
			if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if batches[0].Steps != tc.steps || batches[0].Dropped != tc.dropped {
				t.Fatalf("catch-up batch = %+v", batches[0])
			}
			if batches[1].Steps != 1 || batches[1].Dropped != 0 {
				t.Fatalf("next batch = %+v", batches[1])
			}
			if !reflect.DeepEqual(c.waits, []time.Duration{10 * time.Millisecond, 5 * time.Millisecond}) {
				t.Fatalf("fractional remainder lost: waits = %v", c.waits)
			}
			if s.Tick() != uint64(tc.steps+1) || s.Time() != time.Duration(tc.steps+1)*10*time.Millisecond {
				t.Fatal("discarded wall time advanced simulation")
			}
		})
	}
}

func TestExecutionAndObserverTimeCountTowardNextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var batches []Stats
	var c *fakeClock
	s, r, clock := fixture(t, Config{OnBatch: func(stats Stats) {
		batches = append(batches, stats)
		c.now = c.now.Add(2 * time.Millisecond)
		if len(batches) == 2 {
			cancel()
		}
	}})
	c = clock
	if err := s.Add("work", ecf.SystemFunc(func(*ecf.Frame) error {
		c.now = c.now.Add(4 * time.Millisecond)
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.waits, []time.Duration{10 * time.Millisecond, 4 * time.Millisecond}) {
		t.Fatalf("work caused deadline drift: %v", c.waits)
	}
	for _, stats := range batches {
		if stats.Duration != 4*time.Millisecond {
			t.Fatalf("execution duration = %v", stats.Duration)
		}
	}
}

func TestRunnerFailureAndCancellationBetweenCatchUpSteps(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "error"}[fail], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var batches []Stats
			s, r, c := fixture(t, Config{OnBatch: func(stats Stats) { batches = append(batches, stats) }})
			c.oversleep = time.Second
			want := errors.New("system failed")
			if err := s.Add("stop", ecf.SystemFunc(func(*ecf.Frame) error {
				if fail {
					return want
				}
				cancel()
				return nil
			})); err != nil {
				t.Fatal(err)
			}
			err := r.Run(ctx)
			if fail {
				if !errors.Is(err, want) || batches[0].Steps != 0 {
					t.Fatal(err, batches)
				}
			} else if !errors.Is(err, context.Canceled) || batches[0].Steps != 1 {
				t.Fatal(err, batches)
			}
			if s.Tick() != 1 || !c.closed || r.running.Load() {
				t.Fatal("runner continued after stop")
			}
		})
	}
}

func TestRunnerValidationPreCancellationAndReentry(t *testing.T) {
	if _, err := New(nil, Config{}); err == nil {
		t.Fatal("accepted nil scheduler")
	}
	s, r, c := fixture(t, Config{})
	if _, err := New(s, Config{MaxCatchUpSteps: -1}); err == nil {
		t.Fatal("accepted negative limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Tick() != 0 || len(c.waits) != 0 {
		t.Fatal("pre-canceled runner advanced time")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if err := s.Add("reenter", ecf.SystemFunc(func(*ecf.Frame) error {
		if err := r.Run(ctx); !errors.Is(err, ErrAlreadyRunning) {
			t.Fatal(err)
		}
		cancel()
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := r.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if s.Tick() != 1 {
		t.Fatal("runner did not restart from a clean boundary")
	}
}

func TestSystemClockCancellation(t *testing.T) {
	// Exercise the actual timer cancellation path without relying on sleep timing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &systemClock{}
	defer c.Close()
	if err := c.Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
