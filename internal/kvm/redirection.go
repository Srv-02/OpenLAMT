// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/AMTRedirector.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"io"

	"github.com/Srv-02/openlamt/internal/redir"
)

// The redirection layer lives in internal/redir; these names stay here for internal/ui.
type RedirectionError = redir.RedirectionError

var (
	ErrAuthUnsupported = redir.ErrAuthUnsupported
	ErrFieldTooLong    = redir.ErrFieldTooLong
	ErrAuthDataTooLong = redir.ErrAuthDataTooLong
	ErrRedirProtocol   = redir.ErrRedirProtocol
)

const (
	cmdKVMStart      = 0x40
	cmdKVMStartReply = 0x41
)

// kvmStart runs the KVM start exchange after authentication (AMTRedirector.ts:462-472). On
// return rw carries raw RFB bytes.
func kvmStart(r io.Reader, w io.Writer) error {
	if _, err := w.Write([]byte{cmdKVMStart, 0, 0, 0, 0, 0, 0, 0}); err != nil {
		return err
	}
	var reply [8]byte
	if _, err := io.ReadFull(r, reply[:]); err != nil {
		return err
	}
	if reply[0] != cmdKVMStartReply {
		return ErrRedirProtocol
	}
	return nil
}
