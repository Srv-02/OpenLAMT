// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTRedirector.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

// Package redir implements the protocol-neutral part of the AMT redirection protocol: the TCP/TLS
// connection, StartRedirectionSession for a service (KVM, IDE-R or SOL) and digest authentication.
package redir

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
)

const (
	cmdStartSession      = 0x10
	cmdStartSessionReply = 0x11
	cmdAuthenticate      = 0x13
	cmdAuthenticateReply = 0x14

	authTypeQuery     = 0
	authTypeDigestQOP = 4

	authURI     = "/RedirectionService" // console interceptor.go:639
	maxAuthData = 1024
	maxFieldLen = 255 // one length byte (writeField)
	cnonceLen   = 32

	authNC = "00000001"
)

// Protocol selects the redirection service the session is started for.
type Protocol int

const (
	KVM  Protocol = iota // start magic 10 01 00 00 'K' 'V' 'M' 'R' (AMTRedirector.ts:84)
	IDER                 // start magic 10 00 00 00 'I' 'D' 'E' 'R' (AMTRedirector.ts:86)
	SOL                  // start magic 10 00 00 00 'S' 'O' 'L' ' ' (AMTRedirector.ts:83)
)

var (
	startKVM  = []byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'}
	startIDER = []byte{0x10, 0x00, 0x00, 0x00, 'I', 'D', 'E', 'R'}
	startSOL  = []byte{0x10, 0x00, 0x00, 0x00, 'S', 'O', 'L', ' '}
)

func (p Protocol) startMagic() ([]byte, error) {
	switch p {
	case KVM:
		return startKVM, nil
	case IDER:
		return startIDER, nil
	case SOL:
		return startSOL, nil
	}
	return nil, fmt.Errorf("redirection: unknown protocol %d", int(p))
}

// RedirectionError reports a non-zero status from the device; it never carries credentials.
// Stage is "start" or "auth".
type RedirectionError struct {
	Stage  string
	Status byte
}

func (e *RedirectionError) Error() string {
	return fmt.Sprintf("redirection: %s failed with status %d", e.Stage, e.Status)
}

var (
	ErrAuthUnsupported = errors.New("redirection: device does not offer digest authentication with qop")
	ErrFieldTooLong    = errors.New("redirection: user name or URI longer than 255 bytes")
	ErrAuthDataTooLong = errors.New("redirection: authentication data exceeds 1024 bytes")
	ErrRedirProtocol   = errors.New("redirection: unexpected reply from device")
)

// Handshake runs StartRedirectionSession for p, the authentication query and one digest
// authentication attempt (type 4, nc 00000001). On success the redirection layer is done and rw
// carries the service's own protocol (the caller sends the KVM start, the first IDE-R message or
// the SOL start).
// random supplies cnonce bytes (nil = crypto/rand).
func Handshake(rw io.ReadWriter, p Protocol, user, password string, random io.Reader) error {
	magic, err := p.startMagic()
	if err != nil {
		return err
	}
	if len(user) > maxFieldLen || len(authURI) > maxFieldLen {
		return ErrFieldTooLong
	}

	// StartRedirectionSession (AMTRedirector.ts:168, :247-269)
	if _, err := rw.Write(magic); err != nil {
		return err
	}
	var start [13]byte
	if _, err := io.ReadFull(rw, start[:]); err != nil {
		return err
	}
	if start[0] != cmdStartSessionReply {
		return ErrRedirProtocol
	}
	if start[1] != 0 {
		return &RedirectionError{Stage: "start", Status: start[1]}
	}
	if _, err := io.CopyN(io.Discard, rw, int64(start[12])); err != nil { // oemlen
		return err
	}

	// Authentication query (AMTRedirector.ts:262)
	if _, err := rw.Write([]byte{cmdAuthenticate, 0, 0, 0, authTypeQuery, 0, 0, 0, 0}); err != nil {
		return err
	}
	_, _, types, err := readAuthReply(rw)
	if err != nil {
		return err
	}
	if !bytes.Contains(types, []byte{authTypeDigestQOP}) {
		return ErrAuthUnsupported
	}

	// User message (AMTRedirector.ts:292-301)
	msg := []byte{cmdAuthenticate, 0, 0, 0, authTypeDigestQOP, 0, 0, 0, 0}
	msg = append(msg, byte(len(user)))
	msg = append(msg, user...)
	msg = append(msg, 0, 0, byte(len(authURI)))
	msg = append(msg, authURI...)
	msg = append(msg, 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(msg[5:9], uint32(len(msg)-9))
	if _, err := rw.Write(msg); err != nil {
		return err
	}

	// Challenge: [len]realm [len]nonce [len]qop (status 1)
	authType, status, data, err := readAuthReply(rw)
	if err != nil {
		return err
	}
	if authType != authTypeDigestQOP || status != 1 {
		if status != 0 {
			return &RedirectionError{Stage: "auth", Status: status}
		}
		return ErrRedirProtocol
	}
	fields, err := splitFields(data, 3)
	if err != nil {
		return err
	}
	realm, nonce, qop := fields[0], fields[1], fields[2]

	// Digest message (console writeFields, interceptor.go:687-715)
	cn, err := cnonce(random)
	if err != nil {
		return err
	}
	resp := digestResponse(user, realm, password, nonce, authURI, cn, authNC, qop)
	msg = []byte{cmdAuthenticate, 0, 0, 0, authTypeDigestQOP, 0, 0, 0, 0}
	total := 8
	for _, f := range []string{user, realm, nonce, authURI, cn, authNC, resp, qop} {
		if len(f) > maxFieldLen {
			return ErrFieldTooLong
		}
		msg = append(msg, byte(len(f)))
		msg = append(msg, f...)
		total += len(f)
	}
	binary.LittleEndian.PutUint32(msg[5:9], uint32(total))
	if _, err := rw.Write(msg); err != nil {
		return err
	}

	// Result: exactly one attempt, any non-zero status is final.
	_, status, _, err = readAuthReply(rw)
	if err != nil {
		return err
	}
	if status != 0 {
		return &RedirectionError{Stage: "auth", Status: status}
	}

	return nil
}

// readAuthReply reads one AuthenticateSessionReply (AMTRedirector.ts:275-285). The data length
// is bounded before anything is allocated.
func readAuthReply(r io.Reader) (authType, status byte, data []byte, err error) {
	var h [9]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	if h[0] != cmdAuthenticateReply {
		err = ErrRedirProtocol
		return
	}
	n := binary.LittleEndian.Uint32(h[5:9])
	if n > maxAuthData {
		err = ErrAuthDataTooLong
		return
	}
	status, authType = h[1], h[4]
	data = make([]byte, n)
	_, err = io.ReadFull(r, data)
	return
}

// splitFields parses count length-prefixed strings.
func splitFields(b []byte, count int) ([]string, error) {
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		if len(b) < 1 || len(b) < 1+int(b[0]) {
			return nil, ErrRedirProtocol
		}
		n := int(b[0])
		out = append(out, string(b[1:1+n]))
		b = b[1+n:]
	}
	return out, nil
}

// digestResponse computes the hex MD5 response per RFC 2617 with method POST.
func digestResponse(user, realm, password, nonce, uri, cnonce, nc, qop string) string {
	ha1 := md5hex(user + ":" + realm + ":" + password)
	ha2 := md5hex("POST:" + uri)
	return md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":" + qop + ":" + ha2)
}

func md5hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// cnonce returns 32 lowercase hex characters from random (nil = crypto/rand).
func cnonce(random io.Reader) (string, error) {
	if random == nil {
		random = rand.Reader
	}
	b := make([]byte, cnonceLen/2)
	if _, err := io.ReadFull(random, b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
