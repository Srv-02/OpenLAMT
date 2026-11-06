package typing

import (
	"context"
	"fmt"
	"time"
)

// Type sends the strokes of p through s, one per character (a composed character such as ã is
// a dead key and a letter, sent as one step). It waits delay between steps (not before the
// first, not after the last) and calls progress(done, total) after each character, where total
// is the number of characters; progress may be nil.
// Cancellation is checked only between characters, so modifiers are never left pressed
// and a dead key is always completed with its Space or letter. On a send error already pressed
// modifiers are released on a best-effort basis.
func Type(ctx context.Context, s KeySender, p Plan, delay time.Duration, progress func(done, total int)) error {
	if !p.Sendable() {
		return ErrNotSendable
	}
	total := 0
	for _, st := range p.Strokes {
		if !st.Compose {
			total++
		}
	}
	done := 0
	for i := 0; i < len(p.Strokes); i++ {
		st := p.Strokes[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		if i > 0 && delay > 0 {
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return ctx.Err()
			case <-t.C:
			}
			// both channels may have been ready; never let another stroke out after cancel
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := sendStroke(s, st); err != nil {
			return err
		}
		if st.Dead || st.Compose {
			// not cancellable, so the dead key and the key that completes it (Space, or the
			// base letter of a composed character) stay an atomic pair
			next := Stroke{Keysym: KeySpace}
			if st.Compose {
				i++
				next = p.Strokes[i]
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			if err := sendStroke(s, next); err != nil {
				return err
			}
		}
		done++
		if progress != nil {
			progress(done, total)
		}
	}
	return nil
}

func sendStroke(s KeySender, st Stroke) (err error) {
	var held []uint32
	defer func() {
		if err != nil {
			for j := len(held) - 1; j >= 0; j-- {
				_ = s.SendKey(held[j], false)
			}
		}
	}()
	press := func(k uint32) error {
		// A failed key down may still have reached the target, so it is released too.
		held = append(held, k)
		if err := s.SendKey(k, true); err != nil {
			return fmt.Errorf("typing: key down %#x: %w", k, err)
		}
		return nil
	}
	if st.Shift {
		if err = press(KeyShiftL); err != nil {
			return err
		}
	}
	if st.AltGr {
		if err = press(KeyAltR); err != nil {
			return err
		}
	}
	if err = press(st.Keysym); err != nil {
		return err
	}
	// release in reverse order; keep going after an error so modifiers come up
	for j := len(held) - 1; j >= 0; j-- {
		if e := s.SendKey(held[j], false); e != nil && err == nil {
			err = fmt.Errorf("typing: key up %#x: %w", held[j], e)
		}
	}
	held = nil
	return err
}
