package ecf

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrInvalidStep means a scheduler step duration is not positive.
	ErrInvalidStep = errors.New("ecf: step duration must be positive")
	// ErrScheduleStarted means registration was attempted after stepping began.
	ErrScheduleStarted = errors.New("ecf: system registration is frozen")
	// ErrReentrantStep means Step was called from an active step on this scheduler.
	ErrReentrantStep = errors.New("ecf: scheduler is already stepping")
	// ErrStepPanicked marks a scheduler whose system or command panicked.
	// The original panic propagates; subsequent steps return this error.
	ErrStepPanicked = errors.New("ecf: a previous step panicked")
	// ErrSimulationTimeOverflow means another step would overflow simulation time.
	ErrSimulationTimeOverflow = errors.New("ecf: simulation time exhausted")
)

// Frame describes one system invocation. Time is the end time of the current
// world tick; Delta is the simulation duration since this system last ran.
// Frame and Commands are borrowed for the invocation and must not be retained.
type Frame struct {
	Context  context.Context
	World    *World
	Commands *Commands
	Tick     uint64
	Time     time.Duration
	Delta    time.Duration
}

// System performs simulation work. Return an error to stop the scheduler.
type System interface{ Update(*Frame) error }

// SystemFunc adapts a function to System.
type SystemFunc func(*Frame) error

// Update calls f(frame).
func (f SystemFunc) Update(frame *Frame) error { return f(frame) }

type systemConfig struct{ every uint64 }

// SystemOption configures a registered system.
type SystemOption func(*systemConfig) error

// Every runs a system on ticks n, 2n, 3n, and so on. n must be positive.
func Every(n uint64) SystemOption {
	return func(c *systemConfig) error {
		if n == 0 {
			return errors.New("ecf: system interval must be positive")
		}
		c.every = n
		return nil
	}
}

type scheduledSystem struct {
	name   string
	system System
	every  uint64
	delta  time.Duration
}

// Scheduler advances a world in fixed steps. Use it only on the world's owning
// goroutine; do not copy it. Configure all systems before the first step.
type Scheduler struct {
	world    *World
	step     time.Duration
	systems  []scheduledSystem
	commands *Commands
	frame    Frame
	tick     uint64
	elapsed  time.Duration
	started  bool
	stepping bool
	failed   error
}

// NewScheduler creates a scheduler with a fixed, positive step duration.
func NewScheduler(w *World, step time.Duration) (*Scheduler, error) {
	if w == nil || w.id == 0 {
		return nil, errors.New("ecf: scheduler requires a world created with NewWorld")
	}
	if step <= 0 {
		return nil, ErrInvalidStep
	}
	return &Scheduler{world: w, step: step, commands: NewCommands(w)}, nil
}

// Add registers a uniquely named system in execution order. The default interval
// is every tick. Registration is frozen once the first step begins.
func (s *Scheduler) Add(name string, system System, options ...SystemOption) error {
	if s.started {
		return ErrScheduleStarted
	}
	if name == "" || system == nil {
		return errors.New("ecf: system requires a nonempty name and implementation")
	}
	if f, ok := system.(SystemFunc); ok && f == nil {
		return errors.New("ecf: nil system function")
	}
	for _, existing := range s.systems {
		if existing.name == name {
			return fmt.Errorf("ecf: duplicate system name %q", name)
		}
	}
	config := systemConfig{every: 1}
	for _, option := range options {
		if option == nil {
			return errors.New("ecf: nil system option")
		}
		if err := option(&config); err != nil {
			return err
		}
	}
	if s.step <= 0 || config.every > uint64(math.MaxInt64/int64(s.step)) {
		return errors.New("ecf: system interval exceeds duration range")
	}
	s.systems = append(s.systems, scheduledSystem{
		name: name, system: system, every: config.every, delta: time.Duration(config.every) * s.step,
	})
	return nil
}

// StepDuration returns the fixed world step duration.
func (s *Scheduler) StepDuration() time.Duration { return s.step }

// Tick returns the most recently started tick (0 before stepping). A failed tick
// may be only partially applied; inspect Err before using its state as a snapshot.
func (s *Scheduler) Tick() uint64 { return s.tick }

// Time returns the simulation end time of the most recently started tick.
func (s *Scheduler) Time() time.Duration { return s.elapsed }

// Err returns the permanent failure, if any. A failed scheduler cannot resume.
func (s *Scheduler) Err() error { return s.failed }

// Step advances one world tick without sleeping.
func (s *Scheduler) Step() error { return s.StepContext(context.Background()) }

// StepContext advances one tick, checking cancellation before each system and
// passing ctx to systems for cooperative cancellation. Cancellation before a
// tick starts leaves the scheduler reusable. Cancellation during a tick faults
// it, as does a system/command error. Applied changes are never rolled back.
// Panics propagate and permanently fault the scheduler.
func (s *Scheduler) StepContext(ctx context.Context) error {
	if s.failed != nil {
		return s.failed
	}
	if s.stepping {
		return ErrReentrantStep
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.world.queryDepth != 0 {
		return ErrStructuralMutation
	}
	if s.elapsed > time.Duration(math.MaxInt64)-s.step {
		s.failed = ErrSimulationTimeOverflow
		return s.failed
	}
	s.started, s.stepping = true, true
	finished := false
	defer func() {
		s.stepping = false
		s.commands.Clear()
		s.frame = Frame{}
		if !finished && s.failed == nil {
			s.failed = ErrStepPanicked
		}
	}()
	s.tick++
	s.elapsed += s.step
	for _, entry := range s.systems {
		if err := ctx.Err(); err != nil {
			s.failed = fmt.Errorf("ecf: before system %q at tick %d: %w", entry.name, s.tick, err)
			return s.failed
		}
		if s.tick%entry.every != 0 {
			continue
		}
		s.frame = Frame{Context: ctx, World: s.world, Commands: s.commands,
			Tick: s.tick, Time: s.elapsed, Delta: entry.delta}
		if err := entry.system.Update(&s.frame); err != nil {
			s.failed = fmt.Errorf("ecf: system %q at tick %d: %w", entry.name, s.tick, err)
			return s.failed
		}
		if err := s.commands.Flush(); err != nil {
			s.failed = fmt.Errorf("ecf: commands for system %q at tick %d: %w", entry.name, s.tick, err)
			return s.failed
		}
	}
	finished = true
	return nil
}
