package kvm

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Srv-02/openlamt/internal/kvm/kvmtest"
	"github.com/Srv-02/openlamt/internal/redir"
)

func dialRedirection(t *testing.T, srv *kvmtest.Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

// redirectionHandshake runs the redirection handshake and the KVM start exchange.
func redirectionHandshake(rw net.Conn, user, password string) error {
	if err := redir.Handshake(rw, redir.KVM, user, password, nil); err != nil {
		return err
	}
	return kvmStart(rw, rw)
}

func TestRedirectionHandshake_KVMStart(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	if err := redirectionHandshake(conn, "admin", "Geheim-42"); err != nil {
		t.Fatal(err)
	}
	if srv.Connections() != 1 || srv.AuthType() != 4 {
		t.Fatalf("connections %d authType %d", srv.Connections(), srv.AuthType())
	}
}

func TestRedirectionHandshake_WrongPassword(t *testing.T) {
	srv := kvmtest.Start(t, kvmtest.Options{Username: "admin", Password: "Geheim-42"})
	conn := dialRedirection(t, srv)
	err := redirectionHandshake(conn, "admin", "falsch-1")
	var re *RedirectionError
	if !errors.As(err, &re) || re.Stage != "auth" || re.Status != 1 {
		t.Fatalf("err = %v", err)
	}
	if srv.Connections() != 0 {
		t.Fatalf("connections %d", srv.Connections())
	}
}

func TestRedirectionErrorAliases(t *testing.T) {
	if !errors.Is(ErrAuthUnsupported, redir.ErrAuthUnsupported) || !errors.Is(ErrRedirProtocol, redir.ErrRedirProtocol) ||
		!errors.Is(ErrFieldTooLong, redir.ErrFieldTooLong) || !errors.Is(ErrAuthDataTooLong, redir.ErrAuthDataTooLong) {
		t.Fatal("aliases differ")
	}
	var re *RedirectionError
	if !errors.As(error(&redir.RedirectionError{Stage: "auth", Status: 1}), &re) {
		t.Fatal("RedirectionError alias")
	}
}
