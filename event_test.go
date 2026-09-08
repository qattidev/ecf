package ecf

import (
	"errors"
	"reflect"
	"testing"
)

func mustSubscribe[T any](t *testing.T, w *World, capacity int) *Subscription[T] {
	t.Helper()
	sub, err := Subscribe[T](w, capacity)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func drain[T any](sub *Subscription[T]) []T {
	var result []T
	for {
		value, ok := sub.Next()
		if !ok {
			return result
		}
		result = append(result, value)
	}
}

func TestEventBroadcastBackpressureAndUnsubscribe(t *testing.T) {
	w := NewWorld()
	// Events published before subscription are not replayed.
	if err := Publish(w, 99); err != nil {
		t.Fatal(err)
	}
	a, b := mustSubscribe[int](t, w, 2), mustSubscribe[int](t, w, 2)
	if a.Len() != 0 || b.Len() != 0 {
		t.Fatal("old event replayed")
	}
	for _, value := range []int{1, 2} {
		if err := Publish(w, value); err != nil {
			t.Fatal(err)
		}
	}
	if got := drain(a); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatal(got)
	}
	if err := Publish(w, 3); !errors.Is(err, ErrEventQueueFull) {
		t.Fatalf("Publish = %v", err)
	}
	if a.Len() != 0 || b.Len() != 2 {
		t.Fatal("overflow caused partial delivery")
	}
	if value, ok := b.Next(); !ok || value != 1 {
		t.Fatal("wrong oldest event")
	}
	if err := Publish(w, 3); err != nil {
		t.Fatal(err)
	}
	if got := drain(b); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatal("ring wrap lost FIFO order", got)
	}
	if got := drain(a); !reflect.DeepEqual(got, []int{3}) {
		t.Fatal("fast subscriber missed retry", got)
	}
	a.Close()
	a.Close()
	if err := Publish(w, 4); err != nil {
		t.Fatal(err)
	}
	if a.Len() != 0 || b.Len() != 1 {
		t.Fatal("close affected the wrong subscriber")
	}
	b.Close()
	if _, ok := b.Next(); ok {
		t.Fatal("closed subscriber retained data")
	}
	if err := Publish(w, 5); err != nil {
		t.Fatal(err)
	}
}

func TestEventsCapacityTypesAndWorldIsolation(t *testing.T) {
	w, other := NewWorld(), NewWorld()
	if _, err := Subscribe[int](w, -1); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatal(err)
	}
	a := mustSubscribe[int](t, w, 0)
	b := mustSubscribe[string](t, w, 1)
	c := mustSubscribe[int](t, other, 1)
	if len(a.queue) != DefaultEventCapacity {
		t.Fatal("wrong default capacity")
	}
	if err := Publish(w, 42); err != nil {
		t.Fatal(err)
	}
	if a.Len() != 1 || b.Len() != 0 || c.Len() != 0 {
		t.Fatal("event leaked between types or worlds")
	}
	// Consuming an event releases its references in the backing array.
	pointers := mustSubscribe[*int](t, w, 1)
	value := 1
	if err := Publish(w, &value); err != nil {
		t.Fatal(err)
	}
	if got, ok := pointers.Next(); !ok || got != &value {
		t.Fatal("pointer event changed")
	}
	if pointers.queue[0] != nil {
		t.Fatal("consumed event retained references")
	}
}

func TestResources(t *testing.T) {
	w, other := NewWorld(), NewWorld()
	if _, ok := GetResource[health](w); ok || RemoveResource[health](w) {
		t.Fatal("missing resource present")
	}
	SetResource(w, health{HP: 10})
	p, ok := GetResource[health](w)
	if !ok || p.HP != 10 {
		t.Fatal("resource missing")
	}
	SetResource(w, health{HP: 20})
	if next, _ := GetResource[health](w); next != p || p.HP != 20 {
		t.Fatal("replacement invalidated pointer")
	}
	if _, ok := GetResource[health](other); ok {
		t.Fatal("resource leaked between worlds")
	}
	if !RemoveResource[health](w) || RemoveResource[health](w) {
		t.Fatal("resource removal failed")
	}
	SetResource(w, health{HP: 30})
	if next, _ := GetResource[health](w); next == p || p.HP != 20 {
		t.Fatal("removed resource unexpectedly reattached")
	}
}
