package amt

import (
	"errors"
	"fmt"
	"testing"

	"github.com/device-management-toolkit/go-wsman-messages/v2/pkg/amterror"
)

func TestAccessDenied(t *testing.T) {
	denied := amterror.NewAMTError("b:AccessDenied", "The caller is not authorized.", "")
	other := amterror.NewAMTError("b:DestinationUnreachable", "No route.", "")
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"soap fault AccessDenied", denied, true},
		{"soap fault DestinationUnreachable", other, false},
		{"wrapped soap fault", fmt.Errorf("outer: %w", denied), true},
		{"403", errors.New("wsman.Client post received: 403 Forbidden\n"), true},
		{"500 with AccessDenied body", errors.New("wsman.Client post received: 500 Internal Server Error\n<a:Subcode>b:AccessDenied</a:Subcode>"), true},
		{"500 without AccessDenied", errors.New("wsman.Client post received: 500 Internal Server Error\nboom"), false},
		{"401", errors.New("wsman.Client post received: 401 Unauthorized\n"), false},
		{"unrelated", errors.New("connection refused"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := accessDenied(tc.err, "read audit log")
			if tc.want {
				if !errors.Is(got, ErrAccessDenied) {
					t.Fatalf("got %v, want ErrAccessDenied", got)
				}
				if got.Error() != "read audit log: "+ErrAccessDenied.Error() {
					t.Fatalf("message %q leaks the response", got)
				}
				return
			}
			if got != tc.err {
				t.Fatalf("got %v, want the error unchanged", got)
			}
		})
	}
	if accessDenied(nil, "x") != nil {
		t.Fatal("nil must stay nil")
	}
}
