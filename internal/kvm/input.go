// Ported from github.com/device-management-toolkit/ui-toolkit
// (src/core/Utilities/CommsHelper.ts, src/core/Utilities/MouseHelper.ts, src/core/Utilities/KeyboardHelper.ts), commit e1eb6980963b1e59df45e05edf94b68dd5c36920.
// Copyright (c) Intel Corporation 2019. SPDX-License-Identifier: Apache-2.0
// Modifications Copyright 2026 Michael Mayer, Apache License 2.0.

package kvm

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Button is a pointer button bit as in the RFB PointerEvent button mask.
type Button uint8

const (
	ButtonLeft   Button = 1 << 0
	ButtonMiddle Button = 1 << 1
	ButtonRight  Button = 1 << 2
)

// ErrUnmappedKey is returned for keys without a keysym.
var ErrUnmappedKey = errors.New("kvm: key has no keysym")

// keyEvent builds an RFB KeyEvent (type 4, down flag, 2 padding bytes, keysym).
func keyEvent(sym uint32, down bool) []byte {
	m := make([]byte, 8)
	m[0] = 4
	if down {
		m[1] = 1
	}
	binary.BigEndian.PutUint32(m[4:], sym)
	return m
}

// pointerEvent builds an RFB PointerEvent (type 5, button mask, x, y).
func pointerEvent(x, y int, buttons Button) []byte {
	m := make([]byte, 6)
	m[0] = 5
	m[1] = byte(buttons)
	binary.BigEndian.PutUint16(m[2:], uint16(x))
	binary.BigEndian.PutUint16(m[4:], uint16(y))
	return m
}

// KeyEvent sends one key press or release. Down computes the keysym with the current shift
// state (ShiftLeft or ShiftRight held) and remembers it; a Down of a held key and Up reuse the
// remembered keysym so a key released after shift changed still matches (S6). State lives in
// Client.pressed under inMu, which is held across state change and write so the wire order
// always matches the state (no stuck keys on the remote).
func (c *Client) KeyEvent(k Key, down bool) error {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	if c.pressed == nil {
		c.pressed = make(map[Key]uint32)
	}
	sym, held := c.pressed[k]
	if !held {
		_, l := c.pressed[ShiftLeft]
		_, r := c.pressed[ShiftRight]
		s, ok := Keysym(k, l || r)
		if !ok {
			return ErrUnmappedKey
		}
		sym = s
	}
	if down {
		c.pressed[k] = sym
	} else {
		delete(c.pressed, k)
	}
	return c.send(keyEvent(sym, down))
}

// SendKey sends one key press or release given its unshifted keysym (see KeyForKeysym). It
// goes through KeyEvent, so shift state and held-key bookkeeping match live typing. Together
// with the method set this satisfies typing.KeySender without importing that package.
func (c *Client) SendKey(sym uint32, down bool) error {
	k, ok := KeyForKeysym(sym)
	if !ok {
		return fmt.Errorf("keysym %#x: %w", sym, ErrUnmappedKey)
	}
	return c.KeyEvent(k, down)
}

// ReleaseAll sends Up for every held key (KeyboardHelper.ts:69-76) and clears the state.
func (c *Client) ReleaseAll() error {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	var first error
	for _, s := range c.pressed {
		if err := c.send(keyEvent(s, false)); err != nil && first == nil {
			first = err
		}
	}
	clear(c.pressed)
	return first
}

// PointerEvent moves the pointer to framebuffer coordinates (clamped to the screen) with the
// given buttons held; a click is down then up.
func (c *Client) PointerEvent(x, y int, buttons Button) error {
	w, h := c.fb.Size()
	x = max(0, min(x, w-1))
	y = max(0, min(y, h-1))
	return c.send(pointerEvent(x, y, buttons))
}

// wakeKeysym is X11 F15 (XK_MISCELLANY, which AMT maps to USB HID). Keyboards rarely have it
// and applications ignore it, which is why anti-standby tools press it; it wakes a sleeping
// screen like any key but toggles no state (unlike the lock keys) and types nothing.
const wakeKeysym = 0xffcc

// WakeScreen presses and releases F15 once to wake a host screen that is asleep (moving the
// mouse does not wake every host). It is an OpenLAMT addition: neither MeshCommander nor
// MeshCentral send anything on connect. While the user holds any key, F15 could form a
// combination with it and the host is being woken anyway, so nothing is sent.
func (c *Client) WakeScreen() error {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	if len(c.pressed) > 0 {
		return nil
	}
	if err := c.send(keyEvent(wakeKeysym, true)); err != nil {
		return err
	}
	return c.send(keyEvent(wakeKeysym, false))
}

// CtrlAltDel sends the sequence from CommsHelper.sendCad.
func (c *Client) CtrlAltDel() error {
	c.inMu.Lock()
	defer c.inMu.Unlock()
	for _, s := range []struct {
		sym  uint32
		down bool
	}{{0xffe3, true}, {0xffe9, true}, {0xffff, true}, {0xffff, false}, {0xffe9, false}, {0xffe3, false}} {
		if err := c.send(keyEvent(s.sym, s.down)); err != nil {
			return err
		}
	}
	return nil
}
