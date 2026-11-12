package ui

import "syscall"

// Windows reports Winsock codes; Go's syscall.ECONN* constants are invented values there and
// never match. Each list therefore holds both.
var (
	errnoRefused     = []syscall.Errno{syscall.ECONNREFUSED, 10061} // WSAECONNREFUSED
	errnoUnreachable = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH, 10065, 10051}
	errnoTimedOut    = []syscall.Errno{syscall.ETIMEDOUT, 10060} // WSAETIMEDOUT; Go does not report it as a timeout
	// WSAECONNABORTED: what Windows may report when TCP_MAXRT expires on an established connection
	// ("possibly due to a data transmission time-out"). Kept apart from errnoReset so that it counts
	// as a lost connection only for read and write, never for a dial.
	errnoAborted = []syscall.Errno{10053}
	errnoReset   = []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED, 10054, 10053}
)
