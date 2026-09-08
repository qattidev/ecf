package ecf

import "errors"

// ErrCommandReentry is panicked if a buffer is modified or flushed while flushing.
var ErrCommandReentry = errors.New("ecf: command buffer is already flushing")

// Commands queues world operations for execution outside queries. Operations
// execute in FIFO order without rollback. A buffer belongs to exactly one world.
type Commands struct {
	world    *World
	pending  []func(*World) error
	flushing bool
}

// NewCommands creates a standalone command buffer. A Scheduler supplies its own.
func NewCommands(w *World) *Commands { return &Commands{world: w} }

// Defer queues an operation. Capture entity handles and values, not borrowed
// component pointers. Calling Defer on a flushing buffer or with nil panics.
func (c *Commands) Defer(operation func(*World) error) {
	if c.flushing {
		panic(ErrCommandReentry)
	}
	if operation == nil {
		panic("ecf: nil deferred operation")
	}
	c.pending = append(c.pending, operation)
}

// Len returns the number of pending operations.
func (c *Commands) Len() int { return len(c.pending) }

// Clear discards pending operations. Calling Clear while flushing panics.
func (c *Commands) Clear() {
	if c.flushing {
		panic(ErrCommandReentry)
	}
	clear(c.pending)
	c.pending = c.pending[:0]
}

// Flush executes queued operations. On error or panic it discards all remaining
// operations and preserves changes already applied. It panics during a query or
// recursive flush. Panics from operations propagate to the caller.
func (c *Commands) Flush() error {
	c.world.checkStructural()
	if c.flushing {
		panic(ErrCommandReentry)
	}
	c.flushing = true
	defer func() {
		c.flushing = false
		c.Clear()
	}()
	for i := range c.pending {
		operation := c.pending[i]
		c.pending[i] = nil
		if err := operation(c.world); err != nil {
			return err
		}
	}
	return nil
}
