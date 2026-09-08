// Command pong serves an authoritative ECS Pong simulation and an embedded
// browser client. Run go run ./examples/pong, then visit http://localhost:8080.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8080", "HTTP listen address (use :8080 for LAN play)")
	hz := flag.Int("tick-rate", 60, "simulation ticks per second (10–240)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, *addr, *hz); err != nil {
		log.Fatal(err)
	}
}

func serve(parent context.Context, addr string, hz int) error {
	h, err := newHub(hz)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	server := &http.Server{
		Handler: h.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		IdleTimeout: time.Minute, MaxHeaderBytes: 32 << 10,
	}
	gameDone := make(chan error, 1)
	go func() { gameDone <- h.run(ctx) }()
	httpDone := make(chan error, 1)
	go func() { httpDone <- server.Serve(listener) }()
	log.Printf("Pong: http://%s · %d Hz · open a second browser for two-player mode", listener.Addr(), hz)
	var result error
	gameStopped, httpStopped := false, false
	select {
	case <-parent.Done():
	case result = <-gameDone:
		gameStopped = true
	case result = <-httpDone:
		httpStopped = true
	}
	cancel() // End event streams before waiting for HTTP shutdown.
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
		if result == nil {
			result = fmt.Errorf("HTTP shutdown: %w", err)
		}
	}
	if !gameStopped {
		if err := <-gameDone; result == nil {
			result = err
		}
	}
	if !httpStopped {
		if err := <-httpDone; result == nil {
			result = err
		}
	}
	if errors.Is(result, context.Canceled) || errors.Is(result, http.ErrServerClosed) {
		return nil
	}
	return result
}
