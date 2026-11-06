// Package amt holds the AMT connection configuration and device operations.
package amt

import (
	"context"
	"errors"
	"net"
)

// ErrIncompleteResponse marks a WS-Man answer that lacks the expected element.
var ErrIncompleteResponse = errors.New("device response lacks expected data")

const (
	PortTLS   = "16993"
	PortPlain = "16992"

	RedirectionPortTLS   = "16995"
	RedirectionPortPlain = "16994"
)

// Config describes how to reach one AMT device.
type Config struct {
	Host        string
	Username    string
	Password    string
	UseTLS      bool
	Fingerprint string // hex SHA-256 of the pinned leaf certificate; empty = not pinned yet

	dialContext func(ctx context.Context, network, addr string) (net.Conn, error) // tests only; nil = net.Dialer
}

// Address returns host:port for the configured transport.
func (c Config) Address() string {
	port := PortPlain
	if c.UseTLS {
		port = PortTLS
	}
	return net.JoinHostPort(c.Host, port)
}

// RedirectionAddress returns host:port of the redirection listener (KVM) for the configured transport.
func (c Config) RedirectionAddress() string {
	port := RedirectionPortPlain
	if c.UseTLS {
		port = RedirectionPortTLS
	}
	return net.JoinHostPort(c.Host, port)
}
