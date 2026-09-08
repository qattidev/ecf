package ecf

import (
	"errors"
	"math"
	"math/rand"
	"testing"
)

type position struct{ X, Y float64 }
type velocity struct{ X, Y float64 }
type health struct{ HP int }
type active struct{}

func mustSet[T any](t testing.TB, w *World, e Entity, value T) {
	t.Helper()
	if err := Set(w, e, value); err != nil {
		t.Fatal(err)
	}
}

func assertPanic(t *testing.T, want any, operation func()) {
	t.Helper()
	defer func() {
		if got := recover(); got != want {
			t.Fatalf("panic = %v, want %v", got, want)
		}
	}()
	operation()
	t.Fatal("operation did not panic")
}

func TestEntityLifecycleAndWorldIsolation(t *testing.T) {
	w, other := NewWorld(), NewWorld()
	e, foreign := w.NewEntity(), other.NewEntity()
	mustSet(t, w, e, position{X: 4})
	for _, invalid := range []Entity{{}, foreign} {
		if w.Alive(invalid) || w.Destroy(invalid) || Has[position](w, invalid) || Remove[position](w, invalid) {
			t.Fatalf("accepted invalid entity %v", invalid)
		}
		if err := Set(w, invalid, position{}); !errors.Is(err, ErrInvalidEntity) {
			t.Fatalf("Set invalid = %v", err)
		}
	}
	if !w.Destroy(e) || w.Destroy(e) || w.Len() != 0 {
		t.Fatal("destroy did not remove entity exactly once")
	}
	replacement := w.NewEntity()
	if replacement.index != e.index || replacement == e || w.Alive(e) {
		t.Fatal("recycled index did not invalidate the previous generation")
	}
	if _, ok := Get[position](w, replacement); ok {
		t.Fatal("recycled entity inherited a component")
	}
	if err := Set(w, e, position{}); !errors.Is(err, ErrInvalidEntity) {
		t.Fatal("stale entity accepted after index reuse")
	}
	if w.Len() != 1 || other.Len() != 1 || !other.Alive(foreign) {
		t.Fatal("worlds are not independent")
	}
}

func TestGenerationExhaustionRetiresSlot(t *testing.T) {
	w := NewWorld()
	e := w.NewEntity()
	w.slots[e.index].generation = math.MaxUint32
	e.generation = math.MaxUint32
	if !w.Destroy(e) {
		t.Fatal("destroy failed")
	}
	if next := w.NewEntity(); next.index == e.index {
		t.Fatal("exhausted generation was reused")
	}
}

func TestComponentsAndStorageCompaction(t *testing.T) {
	w := NewWorld()
	entities := []Entity{w.NewEntity(), w.NewEntity(), w.NewEntity()}
	for i, e := range entities {
		mustSet(t, w, e, position{X: float64(i)})
		mustSet(t, w, e, active{})
	}
	p, ok := Get[position](w, entities[0])
	if !ok {
		t.Fatal("component missing")
	}
	mustSet(t, w, entities[0], position{X: 10})
	if p.X != 10 || findStorage[position](w).len() != 3 {
		t.Fatal("replacement should update in place")
	}
	if !Remove[position](w, entities[1]) || Remove[position](w, entities[1]) {
		t.Fatal("remove should report whether a component existed")
	}
	if p, ok := Get[position](w, entities[2]); !ok || p.X != 2 {
		t.Fatal("swap removal corrupted the moved component")
	}
	if !Has[active](w, entities[1]) {
		t.Fatal("removing one component affected another")
	}
	// Nil component values are present values, not absent components.
	mustSet[*position](t, w, entities[0], nil)
	if p, ok := Get[*position](w, entities[0]); !ok || *p != nil {
		t.Fatal("nil pointer component did not round trip")
	}
	// Removal must release references in unused dense storage.
	mustSet(t, w, entities[0], []byte{1, 2, 3})
	s := findStorage[[]byte](w)
	Remove[[]byte](w, entities[0])
	if s.values[:cap(s.values)][0] != nil {
		t.Fatal("removed component retained references")
	}
}

func TestQueryFiltersLateStoresAndReuse(t *testing.T) {
	w := NewWorld()
	query := Query2[position, velocity](w, With[active](), Without[health]())
	count := func() int {
		n := 0
		query.Each(func(_ Entity, p *position, v *velocity) bool {
			p.X += v.X
			n++
			return true
		})
		return n
	}
	if count() != 0 {
		t.Fatal("empty query matched")
	}
	e := w.NewEntity()
	mustSet(t, w, e, position{})
	mustSet(t, w, e, velocity{X: 2})
	if count() != 0 {
		t.Fatal("missing With component matched")
	}
	mustSet(t, w, e, active{})
	if count() != 1 {
		t.Fatal("late component store was not observed")
	}
	mustSet(t, w, e, health{HP: 5})
	if count() != 0 {
		t.Fatal("late exclusion store was not observed")
	}
	Remove[health](w, e)
	if count() != 1 {
		t.Fatal("cached query did not observe removal")
	}
	if p, _ := Get[position](w, e); p.X != 4 {
		t.Fatalf("query pointer updates = %v", p)
	}
	w.Destroy(e)
	replacement := w.NewEntity()
	mustSet(t, w, replacement, position{})
	if count() != 0 {
		t.Fatal("query matched stale component membership")
	}
}

func TestQueryAritiesEarlyExitAndNestedTraversal(t *testing.T) {
	w := NewWorld()
	for i := 0; i < 4; i++ {
		e := w.NewEntity()
		mustSet(t, w, e, position{X: float64(i)})
		mustSet(t, w, e, velocity{})
		mustSet(t, w, e, health{HP: 10})
		mustSet(t, w, e, active{})
	}
	one := Query1[position](w)
	visits := 0
	one.Each(func(_ Entity, _ *position) bool { visits++; return false })
	if visits != 1 || w.queryDepth != 0 {
		t.Fatal("early exit did not release query guard")
	}
	visits = 0
	Query3[position, velocity, health](w).Each(func(_ Entity, _ *position, _ *velocity, h *health) bool {
		h.HP--
		one.Each(func(_ Entity, _ *position) bool { visits++; return true })
		return true
	})
	if visits != 16 {
		t.Fatalf("nested visits = %d", visits)
	}
	visits = 0
	Query4[position, velocity, health, active](w).Each(func(_ Entity, _ *position, _ *velocity, h *health, _ *active) bool {
		if h.HP != 9 {
			t.Fatal("Query3 mutation missing")
		}
		visits++
		return true
	})
	if visits != 4 {
		t.Fatal("Query4 missed entities")
	}
	Query2[position, position](w).Each(func(_ Entity, a, b *position) bool {
		if a != b {
			t.Fatal("repeated component type should reference the same value")
		}
		return true
	})
	Query1[position](w, Without[position]()).Each(func(Entity, *position) bool {
		t.Fatal("contradictory filter matched")
		return false
	})
}

func TestQueryStructuralMutationGuard(t *testing.T) {
	operations := map[string]func(*World, Entity){
		"spawn":   func(w *World, _ Entity) { w.NewEntity() },
		"destroy": func(w *World, e Entity) { w.Destroy(e) },
		"add":     func(w *World, e Entity) { _ = Set(w, e, velocity{}) },
		"remove":  func(w *World, e Entity) { Remove[position](w, e) },
		"flush":   func(w *World, _ Entity) { _ = NewCommands(w).Flush() },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			w := NewWorld()
			e := w.NewEntity()
			mustSet(t, w, e, position{})
			q := Query1[position](w)
			assertPanic(t, ErrStructuralMutation, func() {
				q.Each(func(Entity, *position) bool { operation(w, e); return true })
			})
			if w.queryDepth != 0 || !w.Alive(e) || !Has[position](w, e) || Has[velocity](w, e) {
				t.Fatal("guard failed to preserve structure or recover query depth")
			}
			q.Each(func(Entity, *position) bool {
				mustSet(t, w, e, position{X: 7})
				return true
			})
			w.Destroy(e)
		})
	}
}

func TestQueryTraversalAllocations(t *testing.T) {
	w := NewWorld()
	for range 100 {
		e := w.NewEntity()
		mustSet(t, w, e, position{})
		mustSet(t, w, e, velocity{})
		mustSet(t, w, e, health{})
		mustSet(t, w, e, active{})
	}
	q := Query4[position, velocity, health, active](w)
	allocations := testing.AllocsPerRun(100, func() {
		q.Each(func(_ Entity, p *position, _ *velocity, _ *health, _ *active) bool {
			p.X++
			return true
		})
	})
	if allocations != 0 {
		t.Fatalf("warmed-up traversal allocated %v times", allocations)
	}
}

func TestRandomizedWorldAgainstReferenceModel(t *testing.T) {
	type record struct {
		p    position
		hasP bool
		hasV bool
	}
	w := NewWorld()
	q := Query2[position, velocity](w)
	model := make(map[Entity]record)
	var handles []Entity
	rng := rand.New(rand.NewSource(42))
	for iteration := range 2000 {
		if len(handles) == 0 || rng.Intn(5) == 0 {
			e := w.NewEntity()
			handles = append(handles, e)
			model[e] = record{}
		} else {
			e := handles[rng.Intn(len(handles))]
			rec, alive := model[e]
			switch rng.Intn(4) {
			case 0:
				value := position{X: float64(iteration)}
				err := Set(w, e, value)
				if alive {
					if err != nil {
						t.Fatal(err)
					}
					rec.p, rec.hasP = value, true
					model[e] = rec
				} else if !errors.Is(err, ErrInvalidEntity) {
					t.Fatal("accepted dead handle")
				}
			case 1:
				if alive {
					mustSet(t, w, e, velocity{})
					rec.hasV = true
					model[e] = rec
				}
			case 2:
				if got := Remove[position](w, e); got != (alive && rec.hasP) {
					t.Fatal("remove disagrees with reference")
				}
				if alive {
					rec.hasP = false
					model[e] = rec
				}
			case 3:
				if w.Destroy(e) != alive {
					t.Fatal("destroy disagrees with reference")
				}
				delete(model, e)
			}
		}
		if w.Len() != len(model) {
			t.Fatal("entity count disagrees with reference")
		}
		seen := make(map[Entity]bool)
		q.Each(func(e Entity, p *position, _ *velocity) bool {
			rec, ok := model[e]
			if !ok || !rec.hasP || !rec.hasV || rec.p != *p || seen[e] {
				t.Fatalf("incorrect query match at iteration %d", iteration)
			}
			seen[e] = true
			return true
		})
		for e, rec := range model {
			if seen[e] != (rec.hasP && rec.hasV) {
				t.Fatal("query missed reference match")
			}
		}
	}
}
