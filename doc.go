// Package ecf implements an entity-component system for game servers.
//
// A World owns entities, typed component stores, resources, and event queues.
// Create worlds with NewWorld. A world and everything obtained from it must be
// used by one goroutine at a time and must not be copied. Independent worlds can
// run concurrently. Deliver external inputs through application-owned channels.
//
// Components are ordinary Go values. Queries borrow pointers into component
// storage; reacquire these pointers after structural changes. During a query,
// edit component values directly and defer structural changes with Commands.
// Values containing pointers, maps, or slices retain normal Go aliasing semantics.
//
// Scheduler provides ordered fixed-step execution. Package ecf/tick adds an
// optional wall-clock runner. Neither package handles networking or persistence.
package ecf
