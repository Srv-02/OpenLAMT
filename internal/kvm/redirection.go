// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTRedirector.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

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
	cmdKVMStart          = 0x40
	cmdKVMStartReply     = 0x41

	authTypeQuery     = 0
	authTypeDigestQOP = 4

	authURI     = "/RedirectionService" // console interceptor.go:639
	maxAuthData = 1024
	maxFieldLen = 255 // one length byte (writeField)
	cnonceLen   = 32

	authNC = "00000001"
)

var startKVM = []byte{0x10, 0x01, 0x00, 0x00, 'K', 'V', 'M', 'R'} // AMTRedirector.ts:84

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

// redirectionHandshake runs StartRedirectionSession, the authentication query, one digest
// authentication attempt (type 4, nc 00000001) and the KVM start exchange. On return rw
// carries raw RFB bytes. random supplies cnonce bytes (nil = crypto/rand).
func redirectionHandshake(rw io.ReadWriter, user, password string, random io.Reader) error {
	if len(user) > maxFieldLen || len(authURI) > maxFieldLen {
		return ErrFieldTooLong
	}

	// StartRedirectionSession (AMTRedirector.ts:168, :247-269)
	if _, err := rw.Write(startKVM); err != nil {
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

	// KVM start (AMTRedirector.ts:462-472)
	if _, err := rw.Write([]byte{cmdKVMStart, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var kvmReply [8]byte
	if _, err := io.ReadFull(rw, kvmReply[:]); err != nil {
		return err
	}
	if kvmReply[0] != cmdKVMStartReply {
		return ErrRedirProtocol
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
