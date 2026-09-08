package ecf

import "reflect"

// Filter requires or excludes a component type. Construct it with With or Without.
type Filter struct {
	typeOf  reflect.Type
	exclude bool
}

// With requires an additional component without adding it to the typed callback.
func With[T any]() Filter { return Filter{typeOf: reflect.TypeFor[T]()} }

// Without excludes entities with a component of type T.
func Without[T any]() Filter { return Filter{typeOf: reflect.TypeFor[T](), exclude: true} }

type filterRef struct {
	entry   *componentEntry
	exclude bool
}

type query struct {
	world    *World
	required []componentStore
	filters  []filterRef
}

func newQuery(w *World, required []componentStore, filters []Filter) query {
	q := query{world: w, required: required, filters: make([]filterRef, len(filters))}
	for i, f := range filters {
		if f.typeOf == nil {
			panic("ecf: construct filters with With or Without")
		}
		q.filters[i] = filterRef{entry: w.entry(f.typeOf), exclude: f.exclude}
	}
	return q
}

func (q *query) each(yield func(Entity) bool) {
	driver := q.required[0]
	for _, s := range q.required[1:] {
		if s.len() < driver.len() {
			driver = s
		}
	}
	for _, f := range q.filters {
		if !f.exclude {
			if f.entry.store == nil {
				return
			}
			if f.entry.store.len() < driver.len() {
				driver = f.entry.store
			}
		}
	}
	q.world.queryDepth++
	defer func() { q.world.queryDepth-- }()
	for i := 0; i < driver.len(); i++ {
		e := driver.entityAt(i)
		if q.matches(e) && !yield(e) {
			return
		}
	}
}

func (q *query) matches(e Entity) bool {
	for _, s := range q.required {
		if !s.has(e) {
			return false
		}
	}
	for _, f := range q.filters {
		has := f.entry.store != nil && f.entry.store.has(e)
		if has == f.exclude {
			return false
		}
	}
	return true
}

// View1 is a reusable query over one component type. Do not copy it.
type View1[A any] struct {
	q query
	a *storage[A]
}

// Query1 constructs a query. Construct queries once and reuse them across ticks.
func Query1[A any](w *World, filters ...Filter) *View1[A] {
	a := ensureStorage[A](w)
	return &View1[A]{q: newQuery(w, []componentStore{a}, filters), a: a}
}

// Each visits matching entities in unspecified order. Return false to stop.
// Pointers are borrowed; do not retain them after the callback. Nested queries
// are supported, but structural mutations must wait until all queries finish.
func (v *View1[A]) Each(yield func(Entity, *A) bool) {
	v.q.each(func(e Entity) bool { return yield(e, &v.a.values[v.a.index(e)]) })
}

// View2 is a reusable query over two component types. Do not copy it.
type View2[A, B any] struct {
	q query
	a *storage[A]
	b *storage[B]
}

// Query2 constructs a query requiring both component types and all filters.
func Query2[A, B any](w *World, filters ...Filter) *View2[A, B] {
	a, b := ensureStorage[A](w), ensureStorage[B](w)
	return &View2[A, B]{q: newQuery(w, []componentStore{a, b}, filters), a: a, b: b}
}

// Each visits matching entities with borrowed component pointers; false stops.
func (v *View2[A, B]) Each(yield func(Entity, *A, *B) bool) {
	v.q.each(func(e Entity) bool {
		return yield(e, &v.a.values[v.a.index(e)], &v.b.values[v.b.index(e)])
	})
}

// View3 is a reusable query over three component types. Do not copy it.
type View3[A, B, C any] struct {
	q query
	a *storage[A]
	b *storage[B]
	c *storage[C]
}

// Query3 constructs a query requiring three component types and all filters.
func Query3[A, B, C any](w *World, filters ...Filter) *View3[A, B, C] {
	a, b, c := ensureStorage[A](w), ensureStorage[B](w), ensureStorage[C](w)
	return &View3[A, B, C]{q: newQuery(w, []componentStore{a, b, c}, filters), a: a, b: b, c: c}
}

// Each visits matching entities with borrowed component pointers; false stops.
func (v *View3[A, B, C]) Each(yield func(Entity, *A, *B, *C) bool) {
	v.q.each(func(e Entity) bool {
		return yield(e, &v.a.values[v.a.index(e)], &v.b.values[v.b.index(e)], &v.c.values[v.c.index(e)])
	})
}

// View4 is a reusable query over four component types. Do not copy it.
type View4[A, B, C, D any] struct {
	q query
	a *storage[A]
	b *storage[B]
	c *storage[C]
	d *storage[D]
}

// Query4 constructs a query requiring four component types and all filters.
func Query4[A, B, C, D any](w *World, filters ...Filter) *View4[A, B, C, D] {
	a, b, c, d := ensureStorage[A](w), ensureStorage[B](w), ensureStorage[C](w), ensureStorage[D](w)
	return &View4[A, B, C, D]{
		q: newQuery(w, []componentStore{a, b, c, d}, filters), a: a, b: b, c: c, d: d,
	}
}

// Each visits matching entities with borrowed component pointers; false stops.
func (v *View4[A, B, C, D]) Each(yield func(Entity, *A, *B, *C, *D) bool) {
	v.q.each(func(e Entity) bool {
		return yield(e, &v.a.values[v.a.index(e)], &v.b.values[v.b.index(e)],
			&v.c.values[v.c.index(e)], &v.d.values[v.d.index(e)])
	})
}
