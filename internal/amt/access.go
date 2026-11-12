package amt

import (
	"errors"
	"fmt"
	"strings"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

// ErrAccessDenied: the account lacks the AMT realm for this operation (e.g. no audit log permission).
var ErrAccessDenied = errors.New("access denied: the account lacks the permission for this operation")

const postReceived = "wsman.Client post received"

// accessDenied wraps err with ErrAccessDenied when it is an AMT SOAP fault (errors.As *amterror.AMTError)
// whose SubCode contains "AccessDenied", or a WS-Man client error "wsman.Client post received: …" with
// status 403 or whose body contains "AccessDenied"; otherwise it returns err unchanged (nil stays nil).
// A 401 stays unchanged: the session maps it to ErrAuth. The response body is not part of the result.
func accessDenied(err error, op string) error {
	if err == nil {
		return nil
	}
	var amtErr *amterror.AMTError
	if errors.As(err, &amtErr) {
		if strings.Contains(amtErr.SubCode, "AccessDenied") {
			return fmt.Errorf("%s: %w", op, ErrAccessDenied)
		}
		return err
	}
	msg := err.Error()
	if _, rest, ok := strings.Cut(msg, postReceived+": "); ok {
		if strings.HasPrefix(rest, "401") {
			return err
		}
		if strings.HasPrefix(rest, "403") || strings.Contains(rest, "AccessDenied") {
			return fmt.Errorf("%s: %w", op, ErrAccessDenied)
		}
	}
	return err
}
