// Command economy demonstrates a slower interval system and shared game rules.
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/qattidev/ecf"
)

type Wallet struct{ Coins float64 }
type Production struct{ PerSecond float64 }
type Rules struct{ Multiplier float64 }

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	w := ecf.NewWorld()
	farm := w.NewEntity()
	if err := ecf.Set(w, farm, Wallet{}); err != nil {
		return err
	}
	if err := ecf.Set(w, farm, Production{PerSecond: 4}); err != nil {
		return err
	}
	ecf.SetResource(w, Rules{Multiplier: 1.5})
	scheduler, err := ecf.NewScheduler(w, time.Second/10)
	if err != nil {
		return err
	}
	producers := ecf.Query2[Wallet, Production](w)
	if err := scheduler.Add("production", ecf.SystemFunc(func(f *ecf.Frame) error {
		rules, _ := ecf.GetResource[Rules](w)
		producers.Each(func(_ ecf.Entity, wallet *Wallet, production *Production) bool {
			wallet.Coins += production.PerSecond * rules.Multiplier * f.Delta.Seconds()
			return true
		})
		return nil
	}), ecf.Every(5)); err != nil {
		return err
	}
	for range 100 {
		if err := scheduler.Step(); err != nil {
			return err
		}
	}
	wallet, _ := ecf.Get[Wallet](w, farm)
	fmt.Printf("simulated=%s coins=%.2f\n", scheduler.Time(), wallet.Coins)
	return nil
}
