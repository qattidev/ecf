# Browser Pong

A small authoritative Pong server built with ECF, plus an embedded Canvas client.
The server handles movement, collisions, bots, scoring, and match lifecycle. The
browser sends controls and draws interpolated snapshots.

## Play

From the repository root:

```sh
go run ./examples/pong
```

Open **http://localhost:8080**. No npm install, frontend build, or external assets
are needed. The Go executable embeds the HTML, CSS, and JavaScript.

- The first browser controls the left paddle against a bot.
- A second browser or tab takes the right paddle. Both players see the same court.
- Additional browsers spectate. The oldest spectator takes a vacant seat when a
  player disconnects. Empty seats revert to bots.
- Move your paddle with **W/S**, **↑/↓**, the **mouse**, or **touch** on the court.
- The first player to **7 points** wins. Paddle-edge hits change the bounce angle.
- **New match** resets the court; **Play again** appears after a win. Space also
  starts a rematch after the game ends.
- **Invite a player** copies the current court URL. Participant changes start a
  fresh match so new players do not inherit another player's score or controls.

For another device on your local network, listen on all interfaces:

```sh
go run ./examples/pong -addr :8080 -tick-rate 60
```

Open `http://<server-LAN-IP>:8080` on each device. The default address binds only
to loopback. Stop the server with Ctrl+C. You can change `-tick-rate` from 10 to
240; the default is 60 Hz. This example has one shared court and keeps its state
in memory, with two player seats and up to 30 spectators.

## How it uses ECF

`game.go` defines position, velocity, paddle, and ball components, with a match
resource holding scores and controls. Cached typed queries update the paddles
and ball. Physics uses substeps of at most 1/240 second to avoid tunnelling at
lower server tick rates.

`hub.go` registers three ordered systems: network inputs, Pong simulation, and
browser snapshots. HTTP handlers send bounded actions to the world-owning
goroutine. No handler reads component pointers. Copied snapshots leave the world
through a channel that retains only the latest state for each browser.

`server.go` serves the embedded client and three endpoints:

| Endpoint | Purpose |
| --- | --- |
| `GET /api/events` | SSE stream; a `welcome` event assigns an opaque session token, then `state` events carry snapshots and the client's seat. |
| `POST /api/input` | Submit `{ "sequence": 1, "axis": -1, "target": null }` with an `X-Pong-Session` header. |
| `POST /api/restart` | Restart the match, using the same session header. |

`axis` is in `[-1, 1]`. A non-null `target` in `[0, 1]` selects normalized pointer
position instead. The server limits paddle speed, ignores old input sequences,
and expires controls after 500 ms without fresh input. Spectator tokens cannot
move paddles or restart games. The token lasts only for its event connection;
the client automatically reconnects and receives a new one if the stream fails.

The client sends controls at about 25 Hz and interpolates snapshots with a 50 ms
buffer. Snapshots are sent at roughly 30 Hz, or every tick when the simulation
rate is lower. SSE plus ordinary HTTP requests keeps both the Go library and
this example free of third-party dependencies. The example is a single-court
demo without accounts, lobbies, or persistence.

## Tests

```sh
go test -race ./examples/pong
```

Tests cover paddle and wall collisions, fast-ball tunnelling at several tick
rates, input expiry, scoring, victory, restart, bot behavior, spectator promotion,
input ordering, HTTP streams, validation, embedded assets, and server shutdown.

An optional real-browser smoke test uses Node 22+ and Chrome's DevTools protocol,
with no JavaScript packages to install. Start Pong on port 8080, then launch a
separate Chrome instance with a temporary profile and remote debugging:

```sh
# macOS; use your Chrome executable's path on other platforms.
"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
  --headless=new --remote-debugging-port=9222 \
  --user-data-dir=/tmp/ecf-pong-test-profile \
  --no-first-run --no-default-browser-check about:blank
```

In another terminal:

```sh
node examples/pong/browser-test.mjs
```

Run it against an otherwise empty court. It checks keyboard, mouse, touch,
multiplayer, spectator promotion, scoring, victory, and rematch behavior, and
saves desktop/mobile screenshots under your temporary directory. It closes only
the tabs it creates. `PONG_URL`, `PONG_DEBUGGER_URL`, and `PONG_SCREENSHOTS` can
override the server URL, DevTools URL, and screenshot directory.
