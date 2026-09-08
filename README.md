# ECF (Entity Component Framework)

ECF is an entity-component system for Go game servers. It provides ordinary Go
components, typed queries, ordered systems, shared resources, and buffered events.
An optional runner advances each world at its own fixed tick rate.

Module: `github.com/qattidev/ecf`. Requires Go **1.25.11 or later** and has no
third-party dependencies. This is the initial implementation; no release is
published as part of this repository setup.

## Start with a world

Components can be structs, tags such as `struct{}`, or other Go values. They do
not implement a library interface. Entities are opaque, comparable handles.

```go
package main

import (
	"fmt"
	"time"

	"github.com/qattidev/ecf"
)

type Position struct{ X float64 }
type Velocity struct{ X float64 }

func main() {
	w := ecf.NewWorld()
	entity := w.NewEntity()
	if err := ecf.Set(w, entity, Position{}); err != nil {
		panic(err)
	}
	if err := ecf.Set(w, entity, Velocity{X: 2}); err != nil {
		panic(err)
	}

	// Construct queries once; they observe later component and entity changes.
	moving := ecf.Query2[Position, Velocity](w)
	scheduler, err := ecf.NewScheduler(w, time.Second)
	if err != nil {
		panic(err)
	}
	err = scheduler.Add("movement", ecf.SystemFunc(func(f *ecf.Frame) error {
		moving.Each(func(_ ecf.Entity, p *Position, v *Velocity) bool {
			p.X += v.X * f.Delta.Seconds()
			return true
		})
		return nil
	}))
	if err != nil {
		panic(err)
	}
	for range 3 {
		if err := scheduler.Step(); err != nil {
			panic(err)
		}
	}
	p, _ := ecf.Get[Position](w, entity)
	fmt.Printf("tick=%d x=%.0f\n", scheduler.Tick(), p.X)
}
```

Output: `tick=3 x=6`. The same scenario is a compiled [documentation example](example_test.go).

## Components and queries

| API | Behavior |
| --- | --- |
| `w.NewEntity()` | Create an entity without components. |
| `w.Alive(e)`, `w.Len()` | Inspect entity lifetime and live entity count. |
| `w.Destroy(e)` | Remove an entity and its components; return whether it was alive. |
| `ecf.Set(w, e, value)` | Add or replace a typed component; return an error for an invalid entity. |
| `ecf.Get[T](w, e)` | Return a borrowed `*T` and a presence flag. |
| `ecf.Has[T](w, e)`, `ecf.Remove[T](w, e)` | Check or remove a component, returning a presence flag. |
| `ecf.Query1[T]` through `ecf.Query4[A,B,C,D]` | Build reusable typed queries. |
| `ecf.With[T]()`, `ecf.Without[T]()` | Require or exclude additional component types. |

Queries require every callback component and every additional filter. For
example, `ecf.Query2[Position, Velocity](w, ecf.Without[Stunned]())` visits moving
entities without a `Stunned` component. Return `false` from `Each` to stop early.
For more than four component types, use additional `With` filters and `Get`
inside the callback.

Each component type uses dense value storage with a sparse entity lookup. Queries
drive iteration from the smallest required store. Query construction resolves
types; traversal performs no reflection and allocates no memory after setup.
Entity iteration order is unspecified and can change after removals. Systems
must not depend on it for reproducible simulation.

Destroying and recycling an entity never makes its old handle valid again.
Handles from another world are invalid. Missing components, zero handles, dead
handles, and foreign handles return `false` from lookup/removal operations;
`Set` returns `ErrInvalidEntity`. A present component can itself hold a nil value.

## Mutation and ownership

One goroutine owns a world, its scheduler, queries, resources, event readers, and
command buffers. Independent worlds can run concurrently. Create worlds with
`NewWorld` and schedulers with `NewScheduler`; do not copy them.

`Get` and query callbacks borrow pointers into component storage. Reacquire
`Get` pointers after structural changes, and do not retain query pointers beyond
their callback. Adding/removing a component or creating/destroying an entity is
a structural change. It may reallocate or compact storage.

During queries you may edit existing component values or replace them with
`Set`. Structural changes panic with `ErrStructuralMutation`. Nested queries
are supported and keep this guard active until the outermost callback returns.

Use a command buffer for structural changes requested during iteration:

```go
commands := ecf.NewCommands(w)
expired := ecf.Query1[Expired](w)
expired.Each(func(entity ecf.Entity, _ *Expired) bool {
	commands.Defer(func(w *ecf.World) error {
		w.Destroy(entity)
		return nil
	})
	return true
})
if err := commands.Flush(); err != nil {
	return err
}
```

Systems use `f.Commands`; the scheduler flushes it after each successful system,
so subsequent systems see the changes. Capture entity handles and component
values in commands, not borrowed pointers. Commands run in FIFO order without
rollback. An error or panic discards remaining commands. A standalone buffer can
then be reused; a scheduler that encountered the failure cannot resume. Do not
enqueue into, clear, or recursively flush a buffer while it is flushing.

Normal Go aliasing applies: components and resources containing maps, slices, or
pointers do not gain deep-copy or synchronization behavior.

## Systems and fixed steps

Implement `Update(*ecf.Frame) error` or wrap a function with `ecf.SystemFunc`.
Register systems with unique nonempty names in execution order. Registration
freezes when the first tick starts. All due systems run sequentially.

Use `ecf.Every(n)` on registration for slower systems:

```go
err := scheduler.Add("ai", aiSystem, ecf.Every(6))
```

The default is every tick. `Every(6)` first runs on tick 6, then 12, 18, and so on.
In a 60 Hz world this is approximately 10 Hz. `Frame.Delta` is six world steps,
so movement, production, and cooldown calculations should use this duration.
Intervals must be positive and must fit in `time.Duration`.

`Frame.Tick` starts at 1. `Frame.Time` is simulation time at the end of the current
tick. `Frame.Context` allows cooperative cancellation inside long-running work.
The frame is borrowed and must not be retained after its invocation.

`Scheduler.Step()` runs one fixed tick without sleeping. `StepContext(ctx)` also
checks cancellation between systems. Cancellation before the tick begins leaves
the scheduler reusable. A system error, command error, or mid-tick cancellation
faults it permanently; errors identify the system and tick. Original panics
propagate and also fault the scheduler. `Err()` reports the fault. Applied changes
are not rolled back, and `Tick()`/`Time()` describe the most recently started tick,
which may be partial after failure.

Choose a positive step duration when constructing the scheduler, for example
`time.Second / 20`, `time.Second / 60`, or `time.Second / 120`. It remains fixed for
the scheduler's lifetime. Durations have nanosecond precision, so some frequencies
round slightly. Fixed steps and ordered systems do not guarantee bit-exact replay
or identical numerical results at different rates.

## Optional real-time runner

Import `github.com/qattidev/ecf/tick` to run an existing scheduler:

```go
runner, err := tick.New(scheduler, tick.Config{
	MaxCatchUpSteps: 5,
	OnBatch: func(stats tick.Stats) {
		// Transfer stats to application monitoring without blocking simulation.
	},
})
if err != nil {
	return err
}
return runner.Run(ctx)
```

`Run` blocks on the caller's goroutine; it starts no background workers. The
first tick becomes due after one step duration. It uses monotonic elapsed time
and an accumulator. Work and callback time count toward the next deadline.

The zero config defaults to at most **five catch-up steps per batch**. Excess
whole-step wall time is discarded; the fractional remainder is retained. Dropped
time does not advance simulation ticks, so sustained overload slows simulation
relative to the wall clock. No larger variable simulation step is substituted.

`OnBatch` reports completed steps, execution duration, and discarded backlog on
the owning goroutine, including failed batches. It must not step the scheduler.
Cancellation returns an error matching `context.Canceled` or
`context.DeadlineExceeded`. A runner stopped at a clean tick boundary can be run
again; it resumes simulation counters with a fresh wall-clock baseline. Long
systems must observe `Frame.Context` themselves for prompt cancellation.

## Resources and events

Use `SetResource(w, value)`, `GetResource[T](w)`, and `RemoveResource[T](w)` for one
shared value per type per world. Resource replacement updates the existing value
in place. Removing a resource detaches previously returned pointers.

`Subscribe[T](w, capacity)` creates an independent FIFO reader for future events.
Capacity 0 selects **1,024 events**; negative capacity is rejected. Consume with
`Next()` and release with `Close()` when finished. Readers have no goroutines,
and events remain queued across ticks until read or closed.

`Publish(w, event)` synchronously copies the event to all subscribers. Later
systems can consume it in the same tick, and slower systems can consume their
own copies later. If any reader is full, publishing returns `ErrEventQueueFull`
and delivers to nobody. Handle or propagate this error explicitly. Publishing
without subscribers succeeds and retains nothing.

Event values containing pointers, slices, or maps still share their referenced
data. Treat that data as immutable or copy it in application code.

## Frontends and reusable game packages

Server code owns authentication, protocols, replication, and persistence. Drain
validated input channels or queues in an early system, with a per-tick work
limit. Build application-owned output structs in a later system and send those
values to the networking layer. Deep-copy referenced data before handing it to
another goroutine. Give players and persistent objects application-owned IDs;
an `Entity` is a local runtime handle, not a serializable identity.

Reusable game packages can define components and resources, expose systems, and
offer a registration function that calls `Scheduler.Add`. Construct queries and
subscriptions during setup and close subscriptions during application teardown.
There is no framework lifecycle or plugin interface to implement. Physics,
networking, rendering, persistence, and deterministic replay remain game code.

## Examples and checks

```sh
go run ./examples/combat
go run ./examples/economy
go run ./examples/worlds
go run ./examples/pong # Open http://localhost:8080 to play in a browser.

go test ./...
go test -race ./...
go vet ./...
go test -run '^$' -bench . -benchmem .
```

The [combat example](examples/combat/main.go) combines movement, damage events,
deferred destruction, and an infrequent audit reader. The
[economy example](examples/economy/main.go) uses a resource and interval-based
production. The [worlds example](examples/worlds/main.go) runs 20 Hz and 60 Hz
matches concurrently with channel inputs and copied frontend output.

The [Pong example](examples/pong/README.md) serves a complete browser game with
keyboard, mouse, and touch controls. Play against the computer or open a second
browser for two-player mode. The server uses ECF for simulation and embeds its
Canvas client, so there is no frontend build step.

Benchmarks cover component access, replacement, removal/addition, and queries at
1,000, 10,000, and 100,000 entities. Query numbers describe a complete traversal;
component-operation numbers describe one operation (or one remove/add pair).
Use representative game workloads before choosing a production tick budget.
