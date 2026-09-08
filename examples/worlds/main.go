// Command worlds runs two independent matches with different wall-clock tick
// rates. Inputs and copied output DTOs cross goroutines; world data does not.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/qattidev/ecf"
	"github.com/qattidev/ecf/tick"
)

type Position struct{ X float64 }
type Velocity struct{ X float64 }
type Input struct{ Speed float64 }

// Snapshot is application-owned output that any frontend protocol can encode.
// It contains values, with no borrowed pointers into the ECS.
type Snapshot struct {
	Match    string
	PlayerID string
	Tick     uint64
	X        float64
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshots := make(chan Snapshot, 128)
	errorsOut := make(chan error, 2)
	var workers sync.WaitGroup
	for _, hz := range []int{20, 60} {
		inputs := make(chan Input, 1)
		inputs <- Input{Speed: 5}
		close(inputs)
		workers.Add(1)
		go func() {
			defer workers.Done()
			errorsOut <- runMatch(ctx, hz, inputs, snapshots)
		}()
	}
	go func() {
		workers.Wait()
		close(snapshots)
		close(errorsOut)
	}()
	latest := make(map[string]Snapshot)
	for snapshot := range snapshots {
		latest[snapshot.Match] = snapshot
	}
	for err := range errorsOut {
		if err != nil {
			return err
		}
	}
	for _, hz := range []int{20, 60} {
		name := fmt.Sprintf("match-%dHz", hz)
		snapshot := latest[name]
		fmt.Printf("%s tick=%d x=%.2f\n", name, snapshot.Tick, snapshot.X)
	}
	return nil
}

func runMatch(parent context.Context, hz int, inputs <-chan Input, outputs chan<- Snapshot) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	w := ecf.NewWorld()
	player := w.NewEntity()
	if err := ecf.Set(w, player, Position{}); err != nil {
		return err
	}
	if err := ecf.Set(w, player, Velocity{}); err != nil {
		return err
	}
	scheduler, err := ecf.NewScheduler(w, time.Second/time.Duration(hz))
	if err != nil {
		return err
	}
	if err := scheduler.Add("inputs", ecf.SystemFunc(func(*ecf.Frame) error {
		// Bound input work so a continuously busy producer cannot starve simulation.
		for range 64 {
			select {
			case input, ok := <-inputs:
				if !ok {
					return nil
				}
				if err := ecf.Set(w, player, Velocity{X: input.Speed}); err != nil {
					return err
				}
			default:
				return nil
			}
		}
		return nil
	})); err != nil {
		return err
	}
	moving := ecf.Query2[Position, Velocity](w)
	if err := scheduler.Add("movement", ecf.SystemFunc(func(f *ecf.Frame) error {
		moving.Each(func(_ ecf.Entity, p *Position, v *Velocity) bool {
			p.X += v.X * f.Delta.Seconds()
			return true
		})
		return nil
	})); err != nil {
		return err
	}
	if err := scheduler.Add("output", ecf.SystemFunc(func(f *ecf.Frame) error {
		position, _ := ecf.Get[Position](w, player)
		snapshot := Snapshot{Match: fmt.Sprintf("match-%dHz", hz), PlayerID: "player-1", Tick: f.Tick, X: position.X}
		select {
		case outputs <- snapshot:
			return nil
		case <-f.Context.Done():
			return f.Context.Err()
		}
	})); err != nil {
		return err
	}
	if err := scheduler.Add("finish", ecf.SystemFunc(func(f *ecf.Frame) error {
		if f.Tick >= uint64(hz/5) {
			cancel()
		}
		return nil
	})); err != nil {
		return err
	}
	runner, err := tick.New(scheduler, tick.Config{})
	if err != nil {
		return err
	}
	if err := runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
