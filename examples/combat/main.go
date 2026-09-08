// Command combat demonstrates movement, interval systems, deferred destruction,
// and independent event subscribers using manually driven fixed steps.
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/qattidev/ecf"
)

type Position struct{ X float64 }
type Velocity struct{ X float64 }
type Health struct{ HP int }
type Damage struct {
	Target ecf.Entity
	Amount int
}
type Results struct{ Defeated, DamageEvents int }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	w := ecf.NewWorld()
	target := w.NewEntity()
	if err := ecf.Set(w, target, Position{}); err != nil {
		return err
	}
	if err := ecf.Set(w, target, Velocity{X: 3}); err != nil {
		return err
	}
	if err := ecf.Set(w, target, Health{HP: 3}); err != nil {
		return err
	}
	ecf.SetResource(w, Results{})
	damage, err := ecf.Subscribe[Damage](w, 0)
	if err != nil {
		return err
	}
	defer damage.Close()
	audit, err := ecf.Subscribe[Damage](w, 0)
	if err != nil {
		return err
	}
	defer audit.Close()
	scheduler, err := ecf.NewScheduler(w, time.Second/60)
	if err != nil {
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
	if err := scheduler.Add("attack", ecf.SystemFunc(func(*ecf.Frame) error {
		if w.Alive(target) {
			return ecf.Publish(w, Damage{Target: target, Amount: 1})
		}
		return nil
	}), ecf.Every(6)); err != nil {
		return err
	}
	if err := scheduler.Add("damage", ecf.SystemFunc(func(f *ecf.Frame) error {
		for {
			event, ok := damage.Next()
			if !ok {
				return nil
			}
			hp, ok := ecf.Get[Health](w, event.Target)
			if !ok || hp.HP <= 0 {
				continue
			}
			hp.HP -= event.Amount
			if hp.HP <= 0 {
				// Capture an entity handle, never a borrowed component pointer.
				entity := event.Target
				f.Commands.Defer(func(w *ecf.World) error {
					if w.Destroy(entity) {
						results, _ := ecf.GetResource[Results](w)
						results.Defeated++
					}
					return nil
				})
			}
		}
	})); err != nil {
		return err
	}
	if err := scheduler.Add("audit", ecf.SystemFunc(func(*ecf.Frame) error {
		results, _ := ecf.GetResource[Results](w)
		for {
			if _, ok := audit.Next(); !ok {
				return nil
			}
			results.DamageEvents++
		}
	}), ecf.Every(12)); err != nil {
		return err
	}
	for range 60 {
		if err := scheduler.Step(); err != nil {
			return err
		}
	}
	results, _ := ecf.GetResource[Results](w)
	fmt.Printf("ticks=%d targets=%d damage_events=%d defeated=%d\n",
		scheduler.Tick(), w.Len(), results.DamageEvents, results.Defeated)
	return nil
}
