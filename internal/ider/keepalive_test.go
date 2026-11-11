package ider

import (
	"bytes"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/ider/idertest"
)

// TestMain keeps the keepalive timer out of the wire-exact tests; TestKeepalive shortens it.
func TestMain(m *testing.M) {
	keepaliveInterval = time.Hour
	os.Exit(m.Run())
}

// Without any host read the client pings in its period, AMT's PONGs keep the session (and the
// read deadline) alive, and after Close no ping is sent any more.
func TestKeepalive(t *testing.T) {
	oldK, oldR := keepaliveInterval, readTimeout
	keepaliveInterval, readTimeout = 40*time.Millisecond, 200*time.Millisecond
	t.Cleanup(func() { keepaliveInterval, readTimeout = oldK, oldR })
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})

	start := time.Now()
	pings := 0
	lastSeq := uint32(0)
	for time.Since(start) < 600*time.Millisecond { // 3 x readTimeout without host reads
		m := f.p.Read(t)
		if m.Cmd != cmdPing {
			t.Fatalf("unexpected client message 0x%02x", m.Cmd)
		}
		if pings > 0 && m.Seq != lastSeq+1 {
			t.Fatalf("ping sequence %d after %d", m.Seq, lastSeq)
		}
		lastSeq = m.Seq
		pings++
		f.p.Pong(t)
	}
	if pings < 10 {
		t.Fatalf("%d pings in 600 ms, want >= 10 at 40 ms", pings)
	}
	if err := f.c.Err(); err != nil {
		t.Fatalf("session ended: %v", err)
	}

	if err := f.c.Close(); err != nil {
		t.Fatal(err)
	}
	sent := f.c.pingsSent.Load()
	time.Sleep(5 * keepaliveInterval)
	if got := f.c.pingsSent.Load(); got != sent {
		t.Fatalf("%d pings after Close", got-sent)
	}
	select {
	case <-f.c.pingDone:
	default:
		t.Fatal("ping goroutine still running after Close")
	}
}

// The keepalive timer stops when the session ends on its own (here: the device closes it).
func TestKeepaliveStopsAtSessionEnd(t *testing.T) {
	oldK := keepaliveInterval
	keepaliveInterval = 20 * time.Millisecond
	t.Cleanup(func() { keepaliveInterval = oldK })
	img := newImage(4096)
	f := establish(t, img, fixtureOpts{})
	f.p.Close(t)
	waitDone(t, f.c)
	select {
	case <-f.c.pingDone:
	default:
		t.Fatal("ping goroutine still running after the session ended")
	}
}

// A failed Dial (toggle rejected after the OPEN_SESSION reply started the pinger) leaves no ping
// goroutine behind.
func TestKeepaliveStopsOnDialError(t *testing.T) {
	oldK := keepaliveInterval
	keepaliveInterval = 10 * time.Millisecond
	t.Cleanup(func() { keepaliveInterval = oldK })
	img := newImage(4096)
	err := dialFail(t, img, func(t *testing.T, p *idertest.Peer) {
		p.OpenSessionReply(t, 8192, 8192, 0)
		time.Sleep(50 * time.Millisecond) // pinger is running
		p.StatusData(t, 3, 0)
	})
	if err == nil {
		t.Fatal("expected error")
	}
	buf := make([]byte, 1<<20)
	if n := runtime.Stack(buf, true); bytes.Contains(buf[:n], []byte("startPinger")) {
		t.Fatalf("ping goroutine leaked:\n%s", buf[:n])
	}
}
