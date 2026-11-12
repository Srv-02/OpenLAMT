package ider

import (
	"net"
	"testing"
	"time"
)

// Close must not wait for the TLS close_notify (5 s write deadline) when the peer stopped
// reading and the send buffer is full.
func TestClose_DoesNotWaitForCloseNotify(t *testing.T) {
	f := establish(t, newImage(4096), fixtureOpts{tls: true}) // the peer never reads again

	fillSendBuffer(t, f.c.raw)

	start := time.Now()
	_ = f.c.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close took %v with a stalled peer, want well under the 5 s close_notify deadline", d)
	}
}

// fillSendBuffer writes garbage straight to the raw connection until its send buffer is
// completely full, so that no TLS write is in flight but the next write blocks. The peer does
// not read.
func fillSendBuffer(t *testing.T, raw net.Conn) {
	t.Helper()
	chunk := make([]byte, 64<<10)
	for _, size := range []int{len(chunk), 1} {
		for {
			_ = raw.SetWriteDeadline(time.Now().Add(200 * time.Millisecond))
			if _, err := raw.Write(chunk[:size]); err != nil {
				break
			}
		}
	}
	_ = raw.SetWriteDeadline(time.Time{})
}
