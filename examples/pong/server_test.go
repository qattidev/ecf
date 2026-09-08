package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type eventReader struct {
	body    io.ReadCloser
	scanner *bufio.Scanner
	token   string
}

func startServer(t *testing.T) *httptest.Server {
	t.Helper()
	h, err := newHub(120)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	server := httptest.NewServer(h.handler())
	t.Cleanup(func() {
		cancel()
		server.Close()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
	})
	return server
}

func openEvents(t *testing.T, server *httptest.Server) *eventReader {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(server.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatal("stream did not open", response.Status)
	}
	r := &eventReader{body: response.Body, scanner: bufio.NewScanner(response.Body)}
	name, data := r.next(t)
	var welcome struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal(data, &welcome); err != nil {
		t.Fatal(err)
	}
	if name != "welcome" || welcome.Session == "" {
		t.Fatal("missing welcome session")
	}
	r.token = welcome.Session
	return r
}

func (r *eventReader) next(t *testing.T) (string, []byte) {
	t.Helper()
	var name string
	var data []byte
	for r.scanner.Scan() {
		line := r.scanner.Text()
		if line == "" && name != "" {
			return name, data
		}
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
		}
		if strings.HasPrefix(line, "data: ") {
			data = []byte(strings.TrimPrefix(line, "data: "))
		}
	}
	t.Fatal("stream ended before expected event:", r.scanner.Err())
	return "", nil
}

func (r *eventReader) state(t *testing.T, matches func(snapshot) bool) snapshot {
	t.Helper()
	for {
		name, data := r.next(t)
		if name != "state" {
			continue
		}
		var state snapshot
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatal(err)
		}
		if matches == nil || matches(state) {
			return state
		}
	}
}

func post(t *testing.T, server *httptest.Server, path, token, body, origin string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Pong-Session", token)
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode
}

func TestBrowserProtocolSoloMultiplayerAndPromotion(t *testing.T) {
	server := startServer(t)
	left := openEvents(t, server)
	initial := left.state(t, nil)
	if initial.You != 0 || !initial.Paddles[0].Human || initial.Paddles[1].Human {
		t.Fatal("first browser did not get solo mode")
	}
	if code := post(t, server, "/api/input", left.token, `{"sequence":1,"axis":1,"target":null}`, ""); code != 204 {
		t.Fatal(code)
	}
	left.state(t, func(s snapshot) bool { return s.Paddles[0].Y > 320 })
	right := openEvents(t, server)
	joined := right.state(t, nil)
	if joined.You != 1 || !joined.Paddles[0].Human || !joined.Paddles[1].Human {
		t.Fatal("second browser did not replace bot")
	}
	watcher := openEvents(t, server)
	if watcher.state(t, nil).You != -1 {
		t.Fatal("third browser is not a spectator")
	}
	if code := post(t, server, "/api/restart", watcher.token, "", ""); code != 403 {
		t.Fatal("spectator restarted match", code)
	}
	if code := post(t, server, "/api/input", right.token, `{"sequence":1,"axis":0,"target":0.2}`, ""); code != 204 {
		t.Fatal(code)
	}
	moved := right.state(t, func(s snapshot) bool { return s.Paddles[1].Y < 250 })
	if moved.Paddles[0].Y != courtHeight/2 {
		t.Fatal("right player moved left paddle")
	}
	if code := post(t, server, "/api/restart", left.token, "", ""); code != 204 {
		t.Fatal(code)
	}
	reset := left.state(t, func(s snapshot) bool { return s.Round > joined.Round })
	if reset.Score != [2]int{} || reset.Phase != "serve" {
		t.Fatal("restart did not reset match")
	}
	_ = right.body.Close()
	watcher.state(t, func(s snapshot) bool { return s.You == 1 })
	if code := post(t, server, "/api/input", right.token, `{"sequence":2,"axis":1}`, ""); code != 403 {
		t.Fatal("disconnected token accepted", code)
	}
	_ = watcher.body.Close()
	left.state(t, func(s snapshot) bool { return !s.Paddles[1].Human })
}

func TestHTTPAssetsAndInputValidation(t *testing.T) {
	server := startServer(t)
	for path, contains := range map[string]string{"/": "Meet you at the net.", "/app.js": "EventSource", "/style.css": ".court-wrap", "/healthz": "ok"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || response.StatusCode != 200 || !strings.Contains(string(body), contains) {
			t.Fatalf("asset %s failed", path)
		}
	}
	player := openEvents(t, server)
	for _, body := range []string{
		`{`, `null`, `{"sequence":0}`, `{"sequence":1,"axis":2}`, `{"sequence":1,"target":-0.1}`,
		`{"sequence":1,"target":1.1}`, `{"sequence":1,"position":100}`, `{"sequence":1} {}`,
		`{"sequence":1,"axis":` + strings.Repeat("9", 1100) + `}`,
	} {
		if code := post(t, server, "/api/input", player.token, body, ""); code != 400 {
			t.Fatalf("invalid input returned %d: %.100s", code, body)
		}
	}
	if code := post(t, server, "/api/input", "unknown", `{"sequence":1}`, ""); code != 403 {
		t.Fatal(code)
	}
	if code := post(t, server, "/api/input", player.token, `{"sequence":1}`, "https://another.example"); code != 403 {
		t.Fatal(code)
	}
	if code := post(t, server, "/api/restart", player.token, "", "https://another.example"); code != 403 {
		t.Fatal(code)
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("cross-site event stream accepted")
	}
}

func TestServerShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := serve(ctx, "127.0.0.1:0", 60); err != nil {
		t.Fatal(err)
	}
}
