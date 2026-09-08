package ecf_test

import (
	"fmt"
	"time"

	"github.com/qattidev/ecf"
)

func Example() {
	type Position struct{ X float64 }
	type Velocity struct{ X float64 }
	w := ecf.NewWorld()
	e := w.NewEntity()
	if err := ecf.Set(w, e, Position{}); err != nil {
		panic(err)
	}
	if err := ecf.Set(w, e, Velocity{X: 2}); err != nil {
		panic(err)
	}
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
	p, _ := ecf.Get[Position](w, e)
	fmt.Printf("tick=%d x=%.0f\n", scheduler.Tick(), p.X)
	// Output: tick=3 x=6
}
