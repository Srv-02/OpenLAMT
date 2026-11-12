//go:build !windows

package ui

import "syscall"

var (
	errnoRefused     = []syscall.Errno{syscall.ECONNREFUSED}
	errnoUnreachable = []syscall.Errno{syscall.EHOSTUNREACH, syscall.ENETUNREACH}
	errnoTimedOut    = []syscall.Errno{syscall.ETIMEDOUT}
	errnoAborted     []syscall.Errno // Windows only: see errno_windows.go
	errnoReset       = []syscall.Errno{syscall.ECONNRESET, syscall.ECONNABORTED}
)
