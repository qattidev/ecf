package main

import (
	"math"
	"time"

	"github.com/qattidev/ecf"
)

const (
	courtWidth       = 960.0
	courtHeight      = 600.0
	paddleWidth      = 14.0
	paddleHeight     = 100.0
	paddleSpeed      = 560.0
	botSpeed         = 340.0
	ballRadius       = 9.0
	initialBallSpeed = 420.0
	maxBallSpeed     = 820.0
	winningScore     = 7
	inputTimeout     = 500 * time.Millisecond
)

// These ordinary Go structs are the example's ECS components.
type position struct{ X, Y float64 }
type velocity struct{ X, Y float64 }
type paddle struct{ Side int }
type ball struct{}

type control struct {
	Axis    float64
	Target  *float64 // Normalized pointer position, if pointer control is active.
	Updated time.Duration
}

// Match state is a world resource; transport code never shares its pointers.
type match struct {
	Humans    [2]bool
	Controls  [2]control
	Score     [2]int
	Phase     string
	Countdown float64
	Winner    int
	Rally     int
	BestRally int
	Round     uint64
	Serves    int
}

type game struct {
	world          *ecf.World
	paddleEntities [2]ecf.Entity
	ballEntity     ecf.Entity
	paddles        *ecf.View3[position, velocity, paddle]
	balls          *ecf.View3[position, velocity, ball]
}

func newGame() (*game, error) {
	w := ecf.NewWorld()
	g := &game{world: w}
	for side := range 2 {
		e := w.NewEntity()
		g.paddleEntities[side] = e
		x := 48.0
		if side == 1 {
			x = courtWidth - x
		}
		if err := ecf.Set(w, e, position{X: x, Y: courtHeight / 2}); err != nil {
			return nil, err
		}
		if err := ecf.Set(w, e, velocity{}); err != nil {
			return nil, err
		}
		if err := ecf.Set(w, e, paddle{Side: side}); err != nil {
			return nil, err
		}
	}
	g.ballEntity = w.NewEntity()
	if err := ecf.Set(w, g.ballEntity, position{X: courtWidth / 2, Y: courtHeight / 2}); err != nil {
		return nil, err
	}
	if err := ecf.Set(w, g.ballEntity, velocity{}); err != nil {
		return nil, err
	}
	if err := ecf.Set(w, g.ballEntity, ball{}); err != nil {
		return nil, err
	}
	ecf.SetResource(w, match{Phase: "waiting", Winner: -1})
	g.paddles = ecf.Query3[position, velocity, paddle](w)
	g.balls = ecf.Query3[position, velocity, ball](w)
	return g, nil
}

func (g *game) state() *match {
	m, _ := ecf.GetResource[match](g.world)
	return m
}

func (g *game) setHumans(humans [2]bool) {
	m := g.state()
	if m.Humans == humans {
		return
	}
	m.Humans = humans
	g.restart()
}

func (g *game) restart() {
	m := g.state()
	*m = match{Humans: m.Humans, Winner: -1, Round: m.Round + 1}
	g.paddles.Each(func(_ ecf.Entity, p *position, v *velocity, _ *paddle) bool {
		p.Y, v.Y = courtHeight/2, 0
		return true
	})
	g.serve(-1)
	if !m.Humans[0] && !m.Humans[1] {
		m.Phase = "waiting"
	}
}

func (g *game) serve(direction float64) {
	m := g.state()
	angles := [...]float64{-0.32, 0.24, -0.45, 0.38}
	angle := angles[m.Serves%len(angles)]
	m.Serves++
	m.Phase, m.Countdown, m.Rally = "serve", 1.1, 0
	p, _ := ecf.Get[position](g.world, g.ballEntity)
	v, _ := ecf.Get[velocity](g.world, g.ballEntity)
	*p = position{X: courtWidth / 2, Y: courtHeight / 2}
	*v = velocity{X: direction * initialBallSpeed * math.Cos(angle), Y: initialBallSpeed * math.Sin(angle)}
}

func (g *game) update(f *ecf.Frame) error {
	m := g.state()
	if m.Phase == "waiting" || m.Phase == "finished" {
		return nil
	}
	// Small physics steps prevent fast balls tunnelling through paddles even
	// when the example runs with a low world tick rate.
	remaining := f.Delta.Seconds()
	for remaining > 1e-9 {
		dt := math.Min(remaining, 1.0/240)
		remaining -= dt
		g.movePaddles(m, f.Time, dt)
		if m.Phase == "serve" {
			m.Countdown = math.Max(0, m.Countdown-dt)
			if m.Countdown == 0 {
				m.Phase = "playing"
			}
			continue
		}
		g.moveBall(m, dt)
		if m.Phase == "finished" {
			break
		}
	}
	return nil
}

func (g *game) movePaddles(m *match, now time.Duration, dt float64) {
	bp, _ := ecf.Get[position](g.world, g.ballEntity)
	bv, _ := ecf.Get[velocity](g.world, g.ballEntity)
	g.paddles.Each(func(_ ecf.Entity, p *position, v *velocity, pad *paddle) bool {
		side := pad.Side
		speed := 0.0
		if m.Humans[side] {
			input := m.Controls[side]
			if now-input.Updated <= inputTimeout {
				speed = input.Axis * paddleSpeed
				if input.Target != nil {
					speed = (*input.Target*courtHeight - p.Y) / dt
				}
			}
			speed = clamp(speed, -paddleSpeed, paddleSpeed)
		} else {
			target := courtHeight / 2
			if m.Phase == "playing" && ((side == 0 && bv.X < 0) || (side == 1 && bv.X > 0)) {
				target = bp.Y
			}
			speed = clamp((target-p.Y)/dt, -botSpeed, botSpeed)
		}
		previous := p.Y
		p.Y = clamp(p.Y+speed*dt, paddleHeight/2, courtHeight-paddleHeight/2)
		v.Y = (p.Y - previous) / dt
		return true
	})
}

func (g *game) moveBall(m *match, dt float64) {
	g.balls.Each(func(_ ecf.Entity, p *position, v *velocity, _ *ball) bool {
		p.X += v.X * dt
		p.Y += v.Y * dt
		if p.Y < ballRadius {
			p.Y = 2*ballRadius - p.Y
			v.Y = math.Abs(v.Y)
		} else if p.Y > courtHeight-ballRadius {
			p.Y = 2*(courtHeight-ballRadius) - p.Y
			v.Y = -math.Abs(v.Y)
		}
		g.paddles.Each(func(_ ecf.Entity, pp *position, _ *velocity, pad *paddle) bool {
			toward := (pad.Side == 0 && v.X < 0) || (pad.Side == 1 && v.X > 0)
			if !toward || math.Abs(p.X-pp.X) > paddleWidth/2+ballRadius || math.Abs(p.Y-pp.Y) > paddleHeight/2+ballRadius {
				return true
			}
			direction := 1.0
			if pad.Side == 1 {
				direction = -1
			}
			p.X = pp.X + direction*(paddleWidth/2+ballRadius)
			angle := clamp((p.Y-pp.Y)/(paddleHeight/2), -1, 1) * math.Pi / 3
			speed := math.Min(maxBallSpeed, math.Hypot(v.X, v.Y)*1.06)
			v.X, v.Y = direction*speed*math.Cos(angle), speed*math.Sin(angle)
			m.Rally++
			m.BestRally = max(m.BestRally, m.Rally)
			return true
		})
		if p.X < -ballRadius {
			g.point(1)
		}
		if p.X > courtWidth+ballRadius {
			g.point(0)
		}
		return true
	})
}

func (g *game) point(side int) {
	m := g.state()
	m.Score[side]++
	if m.Score[side] == winningScore {
		m.Phase, m.Winner = "finished", side
		return
	}
	direction := 1.0
	if side == 1 {
		direction = -1
	}
	g.serve(direction)
}

func clamp(value, low, high float64) float64 { return math.Max(low, math.Min(value, high)) }

// Snapshots are copied DTOs, safe for HTTP goroutines to serialize.
type paddleSnapshot struct {
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Human bool    `json:"human"`
}

type ballSnapshot struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type snapshot struct {
	Tick         uint64            `json:"tick"`
	Round        uint64            `json:"round"`
	TickRate     int               `json:"tickRate"`
	You          int               `json:"you"` // 0: left, 1: right, -1: spectator.
	Width        float64           `json:"width"`
	Height       float64           `json:"height"`
	PaddleWidth  float64           `json:"paddleWidth"`
	PaddleHeight float64           `json:"paddleHeight"`
	BallRadius   float64           `json:"ballRadius"`
	Paddles      [2]paddleSnapshot `json:"paddles"`
	Ball         ballSnapshot      `json:"ball"`
	Score        [2]int            `json:"score"`
	TargetScore  int               `json:"targetScore"`
	Phase        string            `json:"phase"`
	Countdown    float64           `json:"countdown"`
	Winner       int               `json:"winner"`
	Rally        int               `json:"rally"`
	BestRally    int               `json:"bestRally"`
}

func (g *game) snapshot(tick uint64, hz int) snapshot {
	m := g.state()
	s := snapshot{Tick: tick, Round: m.Round, TickRate: hz, You: -1,
		Width: courtWidth, Height: courtHeight, PaddleWidth: paddleWidth, PaddleHeight: paddleHeight, BallRadius: ballRadius,
		Score: m.Score, TargetScore: winningScore, Phase: m.Phase, Countdown: m.Countdown,
		Winner: m.Winner, Rally: m.Rally, BestRally: m.BestRally}
	g.paddles.Each(func(_ ecf.Entity, p *position, _ *velocity, pad *paddle) bool {
		s.Paddles[pad.Side] = paddleSnapshot{X: p.X, Y: p.Y, Human: m.Humans[pad.Side]}
		return true
	})
	bp, _ := ecf.Get[position](g.world, g.ballEntity)
	s.Ball = ballSnapshot{X: bp.X, Y: bp.Y}
	return s
}
