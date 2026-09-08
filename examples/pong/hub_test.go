package main

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestSeatsSpectatorsAndOrderedInputs(t *testing.T) {
	h, err := newHub(60)
	if err != nil {
		t.Fatal(err)
	}
	var clients []*client
	for i := range 3 {
		c := &client{id: fmt.Sprint(i), out: make(chan snapshot, 1)}
		clients = append(clients, c)
		if err := h.apply(action{kind: "join", client: c}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if clients[0].side != 0 || clients[1].side != 1 || clients[2].side != -1 {
		t.Fatal("wrong seat assignment")
	}
	m := h.game.state()
	round := m.Round
	if err := h.apply(action{kind: "restart", id: "2"}, 0); !errors.Is(err, errSpectator) {
		t.Fatal(err)
	}
	if m.Round != round {
		t.Fatal("spectator restarted game")
	}
	if err := h.apply(action{kind: "input", id: "0", input: input{Sequence: 2, Axis: 1}}, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := h.apply(action{kind: "input", id: "0", input: input{Sequence: 1, Axis: -1}}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if m.Controls[0].Axis != 1 || m.Controls[0].Updated != time.Second || m.Controls[1].Axis != 0 {
		t.Fatal("late input overwrote state or crossed seats")
	}
	m.Score[0] = 4
	if err := h.apply(action{kind: "leave", id: "0"}, 0); err != nil {
		t.Fatal(err)
	}
	if clients[2].side != 0 || m.Round != round+1 || m.Controls[0].Axis != 0 || m.Score != [2]int{} {
		t.Fatal("promotion inherited previous player's state")
	}
	if err := h.apply(action{kind: "input", id: "0"}, 0); !errors.Is(err, errNoSession) {
		t.Fatal("departed session still controls game")
	}
	if err := h.apply(action{kind: "leave", id: "1"}, 0); err != nil {
		t.Fatal(err)
	}
	if m.Humans != [2]bool{true, false} {
		t.Fatal("empty seat not returned to bot")
	}
	for tick := uint64(1); tick <= 100; tick++ {
		h.broadcast(tick)
	}
	state := <-clients[2].out
	if state.Tick != 100 || state.You != 0 {
		t.Fatal("slow client did not receive newest state")
	}
}

func TestHubCapacityAndConfiguration(t *testing.T) {
	for _, hz := range []int{0, 9, 241} {
		if _, err := newHub(hz); err == nil {
			t.Fatal("accepted unsupported rate")
		}
	}
	h, err := newHub(60)
	if err != nil {
		t.Fatal(err)
	}
	for i := range maxClients {
		if err := h.apply(action{kind: "join", client: &client{id: fmt.Sprint(i), out: make(chan snapshot, 1)}}, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.apply(action{kind: "join", client: &client{id: "extra", out: make(chan snapshot, 1)}}, 0); !errors.Is(err, errFull) {
		t.Fatal(err)
	}
}
