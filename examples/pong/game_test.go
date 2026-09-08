package main

import (
	"math"
	"testing"
	"time"

	"github.com/qattidev/ecf"
)

func testGame(t *testing.T) *game {
	t.Helper()
	g, err := newGame()
	if err != nil {
		t.Fatal(err)
	}
	g.setHumans([2]bool{true, true})
	g.state().Phase = "playing"
	return g
}

func advance(t *testing.T, g *game, dt time.Duration, ticks int) {
	t.Helper()
	for tick := 1; tick <= ticks; tick++ {
		if err := g.update(&ecf.Frame{World: g.world, Time: time.Duration(tick) * dt, Delta: dt}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestBallBouncesAndCannotTunnel(t *testing.T) {
	for _, hz := range []int{10, 20, 60, 120, 240} {
		t.Run(time.Duration(hz).String(), func(t *testing.T) {
			g := testGame(t)
			p, _ := ecf.Get[position](g.world, g.ballEntity)
			v, _ := ecf.Get[velocity](g.world, g.ballEntity)
			*p, *v = position{X: 100, Y: courtHeight/2 + 20}, velocity{X: -maxBallSpeed}
			advance(t, g, time.Second/time.Duration(hz), max(1, hz/5))
			if v.X <= 0 || v.Y <= 0 || g.state().Rally != 1 || g.state().Score != [2]int{} {
				t.Fatalf("paddle failed at %d Hz: p=%+v v=%+v match=%+v", hz, p, v, g.state())
			}
			if math.Hypot(v.X, v.Y) > maxBallSpeed+1e-6 {
				t.Fatal("ball exceeded speed limit")
			}
		})
	}
	g := testGame(t)
	p, _ := ecf.Get[position](g.world, g.ballEntity)
	v, _ := ecf.Get[velocity](g.world, g.ballEntity)
	*p, *v = position{X: courtWidth / 2, Y: ballRadius + 1}, velocity{Y: -300}
	advance(t, g, time.Second/60, 1)
	if p.Y < ballRadius || v.Y <= 0 {
		t.Fatal("top wall did not reflect ball")
	}
	*p, *v = position{X: courtWidth / 2, Y: courtHeight - ballRadius - 1}, velocity{Y: 300}
	advance(t, g, time.Second/60, 1)
	if p.Y > courtHeight-ballRadius || v.Y >= 0 {
		t.Fatal("bottom wall did not reflect ball")
	}
}

func TestScoringServeWinAndRestart(t *testing.T) {
	g := testGame(t)
	p, _ := ecf.Get[position](g.world, g.ballEntity)
	v, _ := ecf.Get[velocity](g.world, g.ballEntity)
	*p, *v = position{X: 0, Y: 20}, velocity{X: -420}
	advance(t, g, 100*time.Millisecond, 1)
	m := g.state()
	if m.Score != [2]int{0, 1} || m.Phase != "serve" || p.X != courtWidth/2 || p.Y != courtHeight/2 {
		t.Fatalf("point did not reset serve: %+v, %+v", m, p)
	}
	advance(t, g, 100*time.Millisecond, 12)
	if m.Phase != "playing" {
		t.Fatal("serve countdown did not finish")
	}
	m.Score[0], m.Phase = winningScore-1, "playing"
	*p, *v = position{X: courtWidth, Y: 20}, velocity{X: 420}
	advance(t, g, 100*time.Millisecond, 1)
	if m.Phase != "finished" || m.Winner != 0 || m.Score[0] != winningScore {
		t.Fatal("match did not end")
	}
	final := *p
	advance(t, g, time.Second, 5)
	if *p != final {
		t.Fatal("finished match kept moving")
	}
	round := m.Round
	g.restart()
	if m.Phase != "serve" || m.Winner != -1 || m.Score != [2]int{} || m.Round != round+1 {
		t.Fatal("restart left old match state")
	}
}

func TestControlsBoundsExpiryAndTickRates(t *testing.T) {
	for _, hz := range []int{10, 20, 60, 120, 240} {
		g := testGame(t)
		g.state().Controls[0] = control{Axis: 1}
		advance(t, g, time.Second/time.Duration(hz), hz/5)
		p, _ := ecf.Get[position](g.world, g.paddleEntities[0])
		if math.Abs(p.Y-(courtHeight/2+paddleSpeed*.2)) > .001 {
			t.Fatalf("paddle differs at %d Hz: %g", hz, p.Y)
		}
	}
	g := testGame(t)
	m := g.state()
	m.Controls[0] = control{Axis: -1}
	p, _ := ecf.Get[position](g.world, g.paddleEntities[0])
	if err := g.update(&ecf.Frame{Time: time.Second, Delta: time.Second / 60}); err != nil {
		t.Fatal(err)
	}
	if p.Y != courtHeight/2 {
		t.Fatal("stale input continued moving")
	}
	target := 0.0
	m.Controls[0] = control{Target: &target}
	advance(t, g, time.Second/60, 30)
	if p.Y != paddleHeight/2 {
		t.Fatalf("pointer target ignored paddle bounds: %g", p.Y)
	}
}

func TestBotsAndEmptyCourt(t *testing.T) {
	g, err := newGame()
	if err != nil {
		t.Fatal(err)
	}
	initial := g.snapshot(0, 60)
	advance(t, g, time.Second, 3)
	if g.snapshot(0, 60) != initial {
		t.Fatal("empty court simulated a match")
	}
	g.setHumans([2]bool{true, false})
	g.state().Phase = "playing"
	bp, _ := ecf.Get[position](g.world, g.ballEntity)
	bv, _ := ecf.Get[velocity](g.world, g.ballEntity)
	*bp, *bv = position{X: 600, Y: 100}, velocity{X: 420}
	advance(t, g, time.Second/60, 1)
	bot, _ := ecf.Get[position](g.world, g.paddleEntities[1])
	if bot.Y >= courtHeight/2 || bot.Y < courtHeight/2-botSpeed/60-1e-6 {
		t.Fatal("bot did not track within its speed limit")
	}
}
