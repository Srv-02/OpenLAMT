package ui

import "syscall"

// Windows reports Winsock codes; Go's syscall.ECONN* constants are invented values there and
// never match. Each list therefore holds both.
var (
	errnoRefused     = []syscall.Errno{syscall.ECONNREFUSED, 10061} // WSAECONNREFUSED
	errnoUnreachable = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH, 10065, 10051}
	errnoReset       = []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED, 10054, 10053}
)
