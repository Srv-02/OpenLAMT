//go:build !linux && !windows

package localme

import "errors"

func platformCheck() (Result, error) { return Result{}, errors.ErrUnsupported }
