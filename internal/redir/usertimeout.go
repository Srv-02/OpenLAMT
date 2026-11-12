package redir

import (
	"log"
	"net"
	"syscall"
	"time"
)

// UserTimeout is how long sent data may stay unacknowledged before the operating system aborts
// the connection. Our heartbeats never fill the send buffer and TCP keepalive never fires while
// they are pending, so without it a lost link is noticed only after the retransmit limit
// (about 15 minutes on Linux). Windows and macOS count whole seconds.
const UserTimeout = 10 * time.Second

// applyUserTimeout sets UserTimeout on the TCP connection under c. Connections that are not
// backed by a socket (tests) are left alone; a failure is logged and the connection is used
// without the detection.
func applyUserTimeout(c net.Conn) {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return
	}
	if err := onSocket(sc, func(fd uintptr) error { return setSockUserTimeout(fd, UserTimeout) }); err != nil {
		log.Printf("redir: could not set the TCP user timeout, a lost connection will be noticed late: %v", err)
	}
}

// onSocket runs f with the socket descriptor of sc.
func onSocket(sc syscall.Conn, f func(fd uintptr) error) error {
	rc, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = f(fd) }); err != nil {
		return err
	}
	return ferr
}
