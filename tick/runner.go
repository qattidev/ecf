// Package tick runs an ecf scheduler against wall-clock time with bounded catch-up.
// Simulation always uses the scheduler's fixed step. Under overload, excess
// wall-clock backlog is discarded without advancing simulation time.
package tick

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/qattidev/ecf"
)

// DefaultMaxCatchUpSteps limits the number of simulation steps in one batch.
const DefaultMaxCatchUpSteps = 5

// ErrAlreadyRunning means Run is already active on this runner.
var ErrAlreadyRunning = errors.New("ecf/tick: runner is already running")

// Stats describes a batch. Steps counts completed steps, Duration measures their
// execution (including a failed attempt), and Dropped is discarded wall time.
type Stats struct {
	Steps    int
	Duration time.Duration
	Dropped  time.Duration
}

// Config configures a runner. Its zero value selects the defaults.
type Config struct {
	// MaxCatchUpSteps must be nonnegative; 0 selects DefaultMaxCatchUpSteps.
	MaxCatchUpSteps int
	// OnBatch runs synchronously on the world's goroutine, including on a failed
	// batch. Keep it quick; transfer stats through a channel for external monitoring.
	OnBatch func(Stats)
}

// Runner owns a scheduler for the duration of Run. Do not step that scheduler or
// access its world from another goroutine while Run is active. Do not copy Runner.
type Runner struct {
	scheduler *ecf.Scheduler
	config    Config
	clock     clock
	running   atomic.Bool
}

// New creates a runner. It does not start a goroutine or advance simulation.
func New(scheduler *ecf.Scheduler, config Config) (*Runner, error) {
	if scheduler == nil || scheduler.StepDuration() <= 0 {
		return nil, errors.New("ecf/tick: a configured scheduler is required")
	}
	if config.MaxCatchUpSteps < 0 {
		return nil, errors.New("ecf/tick: catch-up limit must not be negative")
	}
	if config.MaxCatchUpSteps == 0 {
		config.MaxCatchUpSteps = DefaultMaxCatchUpSteps
	}
	return &Runner{scheduler: scheduler, config: config, clock: &systemClock{}}, nil
}

// Run blocks until cancellation or a scheduler error. The first tick is due
// after one step duration. Cancellation returns ctx.Err (possibly wrapped).
// Each invocation starts with no backlog, preserving the scheduler's simulation
// counters. A scheduler faulted during a partial tick cannot be resumed.
func (r *Runner) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrAlreadyRunning
	}
	defer r.running.Store(false)
	defer r.clock.Close()
	if err := r.scheduler.Err(); err != nil {
		return err
	}
	previous := r.clock.Now()
	var backlog time.Duration
	step := r.scheduler.StepDuration()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := r.clock.Now()
		elapsed := now.Sub(previous)
		previous = now
		if elapsed < 0 {
			return errors.New("ecf/tick: clock moved backwards")
		}
		// Saturate only at the duration representation's limit (about 292 years).
		if elapsed > time.Duration(math.MaxInt64)-backlog {
			backlog = time.Duration(math.MaxInt64)
		} else {
			backlog += elapsed
		}
		if backlog < step {
			if err := r.clock.Wait(ctx, step-backlog); err != nil {
				return err
			}
			continue
		}
		stats := Stats{}
		started := r.clock.Now()
		for backlog >= step && stats.Steps < r.config.MaxCatchUpSteps {
			if err := r.scheduler.StepContext(ctx); err != nil {
				stats.Duration = r.clock.Now().Sub(started)
				r.report(stats)
				return err
			}
			backlog -= step
			stats.Steps++
		}
		stats.Dropped = (backlog / step) * step
		backlog %= step
		stats.Duration = r.clock.Now().Sub(started)
		r.report(stats)
	}
}

func (r *Runner) report(stats Stats) {
	if r.config.OnBatch != nil {
		r.config.OnBatch(stats)
	}
}

// clock is private so timing behavior can be tested without sleeping or making
// a clock abstraction part of the library's public API.
type clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
	Close()
}

type systemClock struct{ timer *time.Timer }

func (*systemClock) Now() time.Time { return time.Now() }

func (c *systemClock) Wait(ctx context.Context, duration time.Duration) error {
	if c.timer == nil {
		c.timer = time.NewTimer(duration)
	} else {
		c.timer.Reset(duration)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.timer.C:
		return nil
	}
}

func (c *systemClock) Close() {
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
}
