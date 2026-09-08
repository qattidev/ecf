package ecf

import "reflect"

type storage[T any] struct {
	entities []Entity
	values   []T
	sparse   []int // Dense index plus one; zero means absent.
}

func (s *storage[T]) len() int              { return len(s.entities) }
func (s *storage[T]) entityAt(i int) Entity { return s.entities[i] }
func (s *storage[T]) has(e Entity) bool     { return s.index(e) >= 0 }

func (s *storage[T]) index(e Entity) int {
	if uint64(e.index) >= uint64(len(s.sparse)) {
		return -1
	}
	i := s.sparse[e.index] - 1
	if i < 0 || s.entities[i] != e {
		return -1
	}
	return i
}

func (s *storage[T]) remove(e Entity) {
	i := s.index(e)
	if i < 0 {
		return
	}
	last := len(s.entities) - 1
	if i != last {
		s.entities[i] = s.entities[last]
		s.values[i] = s.values[last]
		s.sparse[s.entities[i].index] = i + 1
	}
	var zero T
	s.values[last] = zero
	s.entities[last] = Entity{}
	s.values = s.values[:last]
	s.entities = s.entities[:last]
	s.sparse[e.index] = 0
}

func findStorage[T any](w *World) *storage[T] {
	entry := w.stores[reflect.TypeFor[T]()]
	if entry == nil || entry.store == nil {
		return nil
	}
	return entry.store.(*storage[T])
}

func ensureStorage[T any](w *World) *storage[T] {
	entry := w.entry(reflect.TypeFor[T]())
	if entry.store == nil {
		entry.store = &storage[T]{}
	}
	return entry.store.(*storage[T])
}

// Set adds or replaces a component by value. It returns ErrInvalidEntity for an
// invalid handle. Adding a component during a query panics; replacement is safe.
func Set[T any](w *World, e Entity, value T) error {
	if !w.Alive(e) {
		return ErrInvalidEntity
	}
	s := findStorage[T](w)
	if s != nil {
		if i := s.index(e); i >= 0 {
			s.values[i] = value
			return nil
		}
	}
	w.checkStructural()
	if s == nil {
		s = ensureStorage[T](w)
	}
	if n := int(e.index) + 1; n > len(s.sparse) {
		s.sparse = append(s.sparse, make([]int, n-len(s.sparse))...)
	}
	s.entities = append(s.entities, e)
	s.values = append(s.values, value)
	s.sparse[e.index] = len(s.entities)
	return nil
}

// Get borrows a component pointer. Reacquire it after structural changes.
// Missing components and invalid entities return nil, false.
func Get[T any](w *World, e Entity) (*T, bool) {
	if !w.Alive(e) {
		return nil, false
	}
	if s := findStorage[T](w); s != nil {
		if i := s.index(e); i >= 0 {
			return &s.values[i], true
		}
	}
	return nil, false
}

// Has reports whether a live entity has a component of type T.
func Has[T any](w *World, e Entity) bool {
	_, ok := Get[T](w, e)
	return ok
}

// Remove removes a component and reports whether it existed. Removing an
// existing component during a query panics.
func Remove[T any](w *World, e Entity) bool {
	if !w.Alive(e) {
		return false
	}
	s := findStorage[T](w)
	if s == nil || !s.has(e) {
		return false
	}
	w.checkStructural()
	s.remove(e)
	return true
}
