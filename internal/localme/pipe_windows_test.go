//go:build windows

package localme

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestPasswordPipe: the app's pipe hands the password once to the process it started and
// nobody can create a second instance of its name.
func TestPasswordPipe(t *testing.T) {
	name, pipe, err := newPasswordPipe()
	if err != nil {
		t.Fatal(err)
	}
	if !pipeName.MatchString(name) {
		t.Fatalf("pipe name %q", name)
	}
	if h, err := createPipeNamed(name); err == nil {
		windows.CloseHandle(h)
		t.Fatal("a second instance of the pipe was created")
	}
	got := make(chan string, 1)
	go func() {
		r, err := openPipe(name)
		if err != nil {
			got <- "open: " + err.Error()
			return
		}
		defer r.Close()
		b, err := readSecret(r)
		if err != nil {
			got <- "read: " + err.Error()
			return
		}
		got <- string(b)
	}()
	// The client is this test process: the current process stands for the started helper.
	err = servePassword(pipe, windows.CurrentProcess(), []byte("Secr3t!pw"), 10*time.Second)
	windows.CloseHandle(pipe) // the end of the password for the client, as in runHelperFile
	if err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "Secr3t!pw" {
		t.Fatalf("client got %q", s)
	}
}

// TestPasswordPipeRefusesForeignClient: a client that is not the started helper gets nothing.
func TestPasswordPipeRefusesForeignClient(t *testing.T) {
	name, pipe, err := newPasswordPipe()
	if err != nil {
		t.Fatal(err)
	}
	other, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getppid()))
	if err != nil {
		t.Skipf("no parent process to stand for the helper: %v", err)
	}
	defer windows.CloseHandle(other)
	got := make(chan []byte, 1)
	go func() {
		r, err := openPipe(name)
		if err != nil {
			got <- nil
			return
		}
		defer r.Close()
		b, _ := io.ReadAll(r)
		got <- b
	}()
	err = servePassword(pipe, other, []byte("Secr3t!pw"), 10*time.Second)
	windows.CloseHandle(pipe)
	if !errors.Is(err, errForeignPipe) {
		t.Fatalf("serve: %v", err)
	}
	if b := <-got; len(b) != 0 {
		t.Fatalf("foreign client read %q", b)
	}
}

// TestPasswordPipeTimeout: without a client the app stops waiting.
func TestPasswordPipeTimeout(t *testing.T) {
	_, pipe, err := newPasswordPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pipe)
	if err := servePassword(pipe, windows.CurrentProcess(), []byte("Secr3t!pw"), 100*time.Millisecond); !errors.Is(err, errPipeTimeout) {
		t.Fatalf("serve: %v", err)
	}
}
