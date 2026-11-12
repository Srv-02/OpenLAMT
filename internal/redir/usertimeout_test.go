package redir

import (
	"context"
	"runtime"
	"syscall"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/redir/redirtest"
)

// TestDialSetsUserTimeout: a connection made by Dial carries the TCP user timeout, read back from
// the socket, on the platforms that have the option.
func TestDialSetsUserTimeout(t *testing.T) {
	srv := startServer(t, redirtest.MagicIDER, redirtest.Options{Username: "admin", Password: "pw"})
	c, err := Dial(context.Background(), Config{Addr: srv.addr(), Username: "admin", Password: "pw"}, IDER, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var got time.Duration
	err = onSocket(c.Raw.(syscall.Conn), func(fd uintptr) error {
		var err error
		got, err = getSockUserTimeout(fd)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if got != UserTimeout {
			t.Fatalf("user timeout = %v, want %v", got, UserTimeout)
		}
	}
}
