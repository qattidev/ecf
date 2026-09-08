package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"time"
)

//go:embed web/*
var assets embed.FS

func (h *hub) handler() http.Handler {
	web, err := fs.Sub(assets, "web")
	if err != nil {
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/events", h.events)
	mux.HandleFunc("POST /api/input", h.input)
	mux.HandleFunc("POST /api/restart", h.restart)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, "ok\n")
	})
	mux.Handle("GET /", http.FileServer(http.FS(web)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'")
		mux.ServeHTTP(w, r)
	})
}

func sameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host == r.Host
}

func (h *hub) events(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "use the game's own page to join", http.StatusForbidden)
		return
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		http.Error(w, "cannot create session", http.StatusInternalServerError)
		return
	}
	c := &client{id: hex.EncodeToString(token[:]), out: make(chan snapshot, 1), side: -1}
	// Also clean up when a request is canceled while its join action is queued.
	defer h.leave(c.id)
	if err := h.submit(r.Context(), action{kind: "join", client: c}); err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(event string, value any) error {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if err := controller.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	if err := write("welcome", struct {
		Session string `json:"session"`
	}{c.id}); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case state, ok := <-c.out:
			if !ok {
				return
			}
			if err := write("state", state); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := write("heartbeat", struct{}{}); err != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-h.done:
			return
		}
	}
}

func (h *hub) input(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin input is not allowed", http.StatusForbidden)
		return
	}
	var value input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		http.Error(w, "invalid input JSON", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		http.Error(w, "expected one input object", http.StatusBadRequest)
		return
	}
	if value.Sequence == 0 || !finite(value.Axis) || math.Abs(value.Axis) > 1 ||
		(value.Target != nil && (!finite(*value.Target) || *value.Target < 0 || *value.Target > 1)) {
		http.Error(w, "input is outside the allowed range", http.StatusBadRequest)
		return
	}
	if err := h.submit(r.Context(), action{kind: "input", id: r.Header.Get("X-Pong-Session"), input: value}); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *hub) restart(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "cross-origin restart is not allowed", http.StatusForbidden)
		return
	}
	if err := h.submit(r.Context(), action{kind: "restart", id: r.Header.Get("X-Pong-Session")}); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func writeError(w http.ResponseWriter, err error) {
	code := http.StatusServiceUnavailable
	if errors.Is(err, errNoSession) || errors.Is(err, errSpectator) {
		code = http.StatusForbidden
	}
	http.Error(w, err.Error(), code)
}
