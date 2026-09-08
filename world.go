package ecf

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync/atomic"
)

var (
	// ErrInvalidEntity means an entity is dead, zero, or belongs to another world.
	ErrInvalidEntity = errors.New("ecf: invalid entity")
	// ErrStructuralMutation is panicked when storage is changed during a query.
	ErrStructuralMutation = errors.New("ecf: structural mutation during query; use Commands")
)

var worldIDs atomic.Uint64

// Entity is an opaque, comparable, world-local identity. The zero value is invalid.
// Entity is not a persistent or network identifier.
type Entity struct {
	world      uint64
	index      uint32
	generation uint32
}

func (e Entity) String() string {
	return fmt.Sprintf("Entity(%d:%d:%d)", e.world, e.index, e.generation)
}

type entitySlot struct {
	generation uint32
	alive      bool
}

type componentStore interface {
	len() int
	has(Entity) bool
	remove(Entity)
	entityAt(int) Entity
}

// Entries allow a filter to observe a component store first populated after the
// query was constructed, without consulting a type map during iteration.
type componentEntry struct{ store componentStore }

// World is an isolated ECS instance. Construct it with NewWorld; do not copy it.
type World struct {
	id         uint64
	slots      []entitySlot
	free       []uint32
	count      int
	queryDepth int
	stores     map[reflect.Type]*componentEntry
	resources  map[reflect.Type]any
	topics     map[reflect.Type]any
}

// NewWorld creates an empty world without starting any goroutines.
func NewWorld() *World {
	// Refuse exhaustion instead of reusing an identity after integer wraparound.
	var id uint64
	for {
		previous := worldIDs.Load()
		if previous == math.MaxUint64 {
			panic("ecf: world identities exhausted")
		}
		id = previous + 1
		if worldIDs.CompareAndSwap(previous, id) {
			break
		}
	}
	return &World{
		id: id, stores: make(map[reflect.Type]*componentEntry),
		resources: make(map[reflect.Type]any), topics: make(map[reflect.Type]any),
	}
}

func (w *World) checkStructural() {
	if w.queryDepth != 0 {
		panic(ErrStructuralMutation)
	}
}

// NewEntity creates an entity without components. It panics during queries.
func (w *World) NewEntity() Entity {
	w.checkStructural()
	if w.id == 0 {
		panic("ecf: construct worlds with NewWorld")
	}
	var index uint32
	if n := len(w.free); n > 0 {
		index = w.free[n-1]
		w.free = w.free[:n-1]
	} else {
		if uint64(len(w.slots)) > math.MaxUint32 {
			panic("ecf: entity identities exhausted")
		}
		index = uint32(len(w.slots))
		w.slots = append(w.slots, entitySlot{generation: 1})
	}
	w.slots[index].alive = true
	w.count++
	return Entity{world: w.id, index: index, generation: w.slots[index].generation}
}

// Alive reports whether e currently exists in this world.
func (w *World) Alive(e Entity) bool {
	return e.world != 0 && e.world == w.id && uint64(e.index) < uint64(len(w.slots)) &&
		w.slots[e.index].alive && w.slots[e.index].generation == e.generation
}

// Len returns the number of live entities, including entities without components.
func (w *World) Len() int { return w.count }

// Destroy removes an entity and all its components. Invalid entities return false.
// Destroying a live entity during a query panics.
func (w *World) Destroy(e Entity) bool {
	if !w.Alive(e) {
		return false
	}
	w.checkStructural()
	for _, entry := range w.stores {
		if entry.store != nil {
			entry.store.remove(e)
		}
	}
	slot := &w.slots[e.index]
	slot.alive = false
	// Retire a slot at generation exhaustion so old handles never revive.
	if slot.generation < math.MaxUint32 {
		slot.generation++
		w.free = append(w.free, e.index)
	}
	w.count--
	return true
}

func (w *World) entry(t reflect.Type) *componentEntry {
	entry := w.stores[t]
	if entry == nil {
		entry = &componentEntry{}
		w.stores[t] = entry
	}
	return entry
}
