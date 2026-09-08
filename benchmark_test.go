package ecf

import (
	"fmt"
	"testing"
)

func benchmarkWorld(b *testing.B, count int) (*World, []Entity) {
	b.Helper()
	w := NewWorld()
	entities := make([]Entity, count)
	for i := range entities {
		e := w.NewEntity()
		entities[i] = e
		mustSet(b, w, e, position{})
		mustSet(b, w, e, velocity{X: 1, Y: 2})
		mustSet(b, w, e, health{HP: 100})
		mustSet(b, w, e, active{})
	}
	return w, entities
}

func BenchmarkQueries(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			w, _ := benchmarkWorld(b, count)
			b.Run("one", func(b *testing.B) {
				q := Query1[position](w)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					q.Each(func(_ Entity, p *position) bool { p.X++; return true })
				}
			})
			b.Run("two", func(b *testing.B) {
				q := Query2[position, velocity](w)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					q.Each(func(_ Entity, p *position, v *velocity) bool { p.X += v.X; p.Y += v.Y; return true })
				}
			})
			b.Run("four", func(b *testing.B) {
				q := Query4[position, velocity, health, active](w)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					q.Each(func(_ Entity, p *position, v *velocity, h *health, _ *active) bool {
						p.X += v.X
						h.HP++
						return true
					})
				}
			})
		})
	}
}

func BenchmarkComponents(b *testing.B) {
	for _, count := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			w, entities := benchmarkWorld(b, count)
			b.Run("get", func(b *testing.B) {
				b.ReportAllocs()
				var i int
				for b.Loop() {
					p, ok := Get[position](w, entities[i%count])
					if !ok {
						b.Fatal("missing component")
					}
					p.X++
					i++
				}
			})
			b.Run("replace", func(b *testing.B) {
				b.ReportAllocs()
				var i int
				for b.Loop() {
					if err := Set(w, entities[i%count], position{X: float64(i)}); err != nil {
						b.Fatal(err)
					}
					i++
				}
			})
			b.Run("remove_add", func(b *testing.B) {
				b.ReportAllocs()
				var i int
				for b.Loop() {
					e := entities[i%count]
					if !Remove[position](w, e) {
						b.Fatal("missing component")
					}
					if err := Set(w, e, position{}); err != nil {
						b.Fatal(err)
					}
					i++
				}
			})
		})
	}
}
