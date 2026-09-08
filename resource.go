package ecf

import "reflect"

// SetResource stores one world-level value of type T. Replacing an existing
// resource updates its value in place, preserving pointers returned by GetResource.
func SetResource[T any](w *World, value T) {
	t := reflect.TypeFor[T]()
	if existing, ok := w.resources[t]; ok {
		*existing.(*T) = value
		return
	}
	p := new(T)
	*p = value
	w.resources[t] = p
}

// GetResource returns a pointer to a world-level resource, or nil, false.
// Removal detaches previously returned pointers from the world.
func GetResource[T any](w *World) (*T, bool) {
	value, ok := w.resources[reflect.TypeFor[T]()]
	if !ok {
		return nil, false
	}
	return value.(*T), true
}

// RemoveResource removes the resource of type T and reports whether it existed.
func RemoveResource[T any](w *World) bool {
	t := reflect.TypeFor[T]()
	if _, ok := w.resources[t]; !ok {
		return false
	}
	delete(w.resources, t)
	return true
}
