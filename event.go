package ecf

import (
	"errors"
	"reflect"
)

// DefaultEventCapacity is the per-subscriber queue capacity when Subscribe gets 0.
const DefaultEventCapacity = 1024

var (
	// ErrEventQueueFull means publishing could not deliver to every subscriber.
	ErrEventQueueFull = errors.New("ecf: event subscriber queue is full")
	// ErrInvalidCapacity means a subscription capacity was negative.
	ErrInvalidCapacity = errors.New("ecf: event capacity must not be negative")
)

type eventTopic[T any] struct{ subscribers []*Subscription[T] }

// Subscription is a bounded FIFO queue receiving future events of type T.
// It belongs to its world's goroutine, must not be copied, and holds events until
// Next or Close. Close subscriptions when no longer needed.
type Subscription[T any] struct {
	topic *eventTopic[T]
	queue []T
	head  int
	size  int
}

// Subscribe creates an independent event reader. Capacity 0 selects
// DefaultEventCapacity; negative capacity returns ErrInvalidCapacity.
func Subscribe[T any](w *World, capacity int) (*Subscription[T], error) {
	if capacity < 0 {
		return nil, ErrInvalidCapacity
	}
	if capacity == 0 {
		capacity = DefaultEventCapacity
	}
	t := reflect.TypeFor[T]()
	value, ok := w.topics[t]
	var topic *eventTopic[T]
	if ok {
		topic = value.(*eventTopic[T])
	} else {
		topic = &eventTopic[T]{}
		w.topics[t] = topic
	}
	sub := &Subscription[T]{topic: topic, queue: make([]T, capacity)}
	topic.subscribers = append(topic.subscribers, sub)
	return sub, nil
}

// Publish synchronously copies an event to each subscriber. If any queue is full,
// it returns ErrEventQueueFull without delivering to anyone. With no subscribers
// it succeeds and retains nothing. Maps, slices, and pointers within an event are
// shared: treat referenced data as immutable or make application-level copies.
func Publish[T any](w *World, event T) error {
	value, ok := w.topics[reflect.TypeFor[T]()]
	if !ok {
		return nil
	}
	topic := value.(*eventTopic[T])
	for _, sub := range topic.subscribers {
		if sub.size == len(sub.queue) {
			return ErrEventQueueFull
		}
	}
	for _, sub := range topic.subscribers {
		sub.queue[(sub.head+sub.size)%len(sub.queue)] = event
		sub.size++
	}
	return nil
}

// Next consumes the oldest queued event. An empty or closed subscription returns
// the zero value and false. Events do not expire between simulation ticks.
func (s *Subscription[T]) Next() (T, bool) {
	var zero T
	if s.size == 0 {
		return zero, false
	}
	value := s.queue[s.head]
	s.queue[s.head] = zero
	s.head = (s.head + 1) % len(s.queue)
	s.size--
	return value, true
}

// Len returns the number of unread events.
func (s *Subscription[T]) Len() int { return s.size }

// Close unsubscribes and releases unread events. It is safe to call repeatedly.
func (s *Subscription[T]) Close() {
	if s.topic == nil {
		return
	}
	subs := s.topic.subscribers
	for i, sub := range subs {
		if sub == s {
			last := len(subs) - 1
			subs[i] = subs[last]
			subs[last] = nil
			s.topic.subscribers = subs[:last]
			break
		}
	}
	s.topic = nil
	s.queue = nil
	s.head, s.size = 0, 0
}
