package ecf

import (
	"errors"
	"reflect"
	"testing"
)

func TestDeferredStructuralChangesAndFIFO(t *testing.T) {
	w := NewWorld()
	commands := NewCommands(w)
	for range 10 {
		e := w.NewEntity()
		mustSet(t, w, e, active{})
	}
	visited := 0
	Query1[active](w).Each(func(e Entity, _ *active) bool {
		commands.Defer(func(w *World) error { w.Destroy(e); return nil })
		visited++
		return true
	})
	if visited != 10 || w.Len() != 10 || commands.Len() != 10 {
		t.Fatal("commands were applied during iteration")
	}
	var order []int
	for i := range 3 {
		commands.Defer(func(w *World) error { order = append(order, i); w.NewEntity(); return nil })
	}
	if err := commands.Flush(); err != nil {
		t.Fatal(err)
	}
	if w.Len() != 3 || commands.Len() != 0 || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal("commands did not execute in FIFO order")
	}
}

func TestCommandFailureAndBufferReuse(t *testing.T) {
	w := NewWorld()
	commands := NewCommands(w)
	want := errors.New("operation failed")
	commands.Defer(func(w *World) error { w.NewEntity(); return nil })
	commands.Defer(func(*World) error { return want })
	commands.Defer(func(w *World) error { w.NewEntity(); return nil })
	if err := commands.Flush(); !errors.Is(err, want) {
		t.Fatalf("Flush = %v", err)
	}
	if w.Len() != 1 || commands.Len() != 0 {
		t.Fatal("failure handling changed applied work")
	}
	commands.Defer(func(w *World) error { w.NewEntity(); return nil })
	if err := commands.Flush(); err != nil || w.Len() != 2 {
		t.Fatal("buffer cannot be reused")
	}
	commands.Defer(func(*World) error { panic(want) })
	assertPanic(t, want, func() { _ = commands.Flush() })
	if commands.Len() != 0 || commands.flushing {
		t.Fatal("panic retained pending work")
	}
}

func TestCommandReentry(t *testing.T) {
	for _, action := range []string{"defer", "flush", "clear"} {
		t.Run(action, func(t *testing.T) {
			commands := NewCommands(NewWorld())
			commands.Defer(func(*World) error {
				switch action {
				case "defer":
					commands.Defer(func(*World) error { return nil })
				case "flush":
					return commands.Flush()
				case "clear":
					commands.Clear()
				}
				return nil
			})
			assertPanic(t, ErrCommandReentry, func() { _ = commands.Flush() })
			if commands.Len() != 0 {
				t.Fatal("pending commands survived panic")
			}
		})
	}
}
