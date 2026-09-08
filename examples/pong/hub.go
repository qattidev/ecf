package main

import (
	"context"
	"errors"
	"time"

	"github.com/qattidev/ecf"
	"github.com/qattidev/ecf/tick"
)

var (
	errNoSession = errors.New("session is no longer connected")
	errSpectator = errors.New("spectators cannot control the match")
	errFull      = errors.New("the court is full; try again shortly")
	errBusy      = errors.New("the server is busy; try again shortly")
)

const maxClients = 32

type input struct {
	Sequence uint64   `json:"sequence"`
	Axis     float64  `json:"axis"`
	Target   *float64 `json:"target"`
}

type client struct {
	id       string
	out      chan snapshot
	side     int
	sequence uint64
}

type action struct {
	kind   string
	client *client
	id     string
	input  input
	ack    chan error
}

// The hub is a scheduler system. Only its owning simulation goroutine accesses
// game state, client membership, and controls. Handlers communicate via actions.
type hub struct {
	game      *game
	scheduler *ecf.Scheduler
	runner    *tick.Runner
	hz        int
	actions   chan action
	done      chan struct{}
	clients   []*client // Join order also determines spectator promotion order.
	seats     [2]string
}

func newHub(hz int) (*hub, error) {
	if hz < 10 || hz > 240 {
		return nil, errors.New("tick-rate must be between 10 and 240")
	}
	g, err := newGame()
	if err != nil {
		return nil, err
	}
	s, err := ecf.NewScheduler(g.world, time.Second/time.Duration(hz))
	if err != nil {
		return nil, err
	}
	h := &hub{game: g, scheduler: s, hz: hz, actions: make(chan action, 128), done: make(chan struct{})}
	if err := s.Add("network inputs", ecf.SystemFunc(h.update)); err != nil {
		return nil, err
	}
	if err := s.Add("pong simulation", ecf.SystemFunc(g.update)); err != nil {
		return nil, err
	}
	if err := s.Add("browser snapshots", ecf.SystemFunc(func(f *ecf.Frame) error {
		h.broadcast(f.Tick)
		return nil
	}), ecf.Every(uint64(max(1, hz/30)))); err != nil {
		return nil, err
	}
	h.runner, err = tick.New(s, tick.Config{})
	return h, err
}

func (h *hub) run(ctx context.Context) error {
	defer func() {
		close(h.done)
		for _, c := range h.clients {
			close(c.out)
		}
	}()
	return h.runner.Run(ctx)
}

func (h *hub) update(f *ecf.Frame) error {
	// A busy network must not starve the physics system.
	for range cap(h.actions) {
		select {
		case a := <-h.actions:
			err := h.apply(a, f.Time)
			if a.ack != nil {
				a.ack <- err
			}
		default:
			return nil
		}
	}
	return nil
}

func (h *hub) apply(a action, now time.Duration) error {
	if a.kind == "join" {
		if len(h.clients) >= maxClients {
			return errFull
		}
		a.client.side = -1
		h.clients = append(h.clients, a.client)
		h.assignSeats()
		h.broadcast(h.scheduler.Tick())
		return nil
	}
	var c *client
	var index int
	for i, candidate := range h.clients {
		if candidate.id == a.id {
			c, index = candidate, i
			break
		}
	}
	if c == nil {
		return errNoSession
	}
	if a.kind == "leave" {
		copy(h.clients[index:], h.clients[index+1:])
		h.clients[len(h.clients)-1] = nil
		h.clients = h.clients[:len(h.clients)-1]
		close(c.out)
		h.assignSeats()
		h.broadcast(h.scheduler.Tick())
		return nil
	}
	if c.side < 0 {
		return errSpectator
	}
	if a.kind == "restart" {
		h.game.restart()
		return nil
	}
	if a.kind == "input" && a.input.Sequence > c.sequence {
		c.sequence = a.input.Sequence
		h.game.state().Controls[c.side] = control{Axis: a.input.Axis, Target: a.input.Target, Updated: now}
	}
	return nil
}

func (h *hub) assignSeats() {
	var occupied [2]bool
	for _, c := range h.clients {
		if c.side >= 0 {
			occupied[c.side] = true
		}
	}
	for _, c := range h.clients {
		if c.side >= 0 {
			continue
		}
		for side := range 2 {
			if !occupied[side] {
				c.side, occupied[side] = side, true
				break
			}
		}
	}
	var seats [2]string
	for _, c := range h.clients {
		if c.side >= 0 {
			seats[c.side] = c.id
		}
	}
	// A promoted spectator must not inherit a departed player's controls/score.
	if seats != h.seats {
		h.seats = seats
		h.game.state().Humans = occupied
		h.game.restart()
	}
}

func (h *hub) broadcast(tick uint64) {
	base := h.game.snapshot(tick, h.hz)
	for _, c := range h.clients {
		state := base
		state.You = c.side
		// Keep only the newest state. A slow browser never blocks simulation.
		select {
		case c.out <- state:
		default:
			select {
			case <-c.out:
			default:
			}
			select {
			case c.out <- state:
			default:
			}
		}
	}
}

func (h *hub) submit(ctx context.Context, a action) error {
	a.ack = make(chan error, 1)
	select {
	case <-h.done:
		return errBusy
	case <-ctx.Done():
		return ctx.Err()
	case h.actions <- a:
	default:
		return errBusy
	}
	select {
	case err := <-a.ack:
		return err
	case <-h.done:
		return errBusy
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *hub) leave(id string) {
	select {
	case h.actions <- action{kind: "leave", id: id}:
	case <-h.done:
	}
}
