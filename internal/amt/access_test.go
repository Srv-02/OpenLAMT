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

func TestIsAccessDenied(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"e:AccessDenied fault", amterror.NewAMTError("e:AccessDenied", "x", ""), true},
		{"b:AccessDenied fault", amterror.NewAMTError("b:AccessDenied", "x", ""), true},
		{"403", errors.New("wsman.Client post received: 403 Forbidden\n"), true},
		{"wrapped ErrAccessDenied", fmt.Errorf("op: %w", ErrAccessDenied), true},
		{"DestinationUnreachable", amterror.NewAMTError("b:DestinationUnreachable", "x", ""), false},
		{"401", errors.New("wsman.Client post received: 401 Unauthorized\n"), false},
		{"500", errors.New("wsman.Client post received: 500 Internal Server Error\nboom"), false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAccessDenied(tc.err); got != tc.want {
				t.Fatalf("IsAccessDenied = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCoreClassDenied(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"e:AccessDenied fault", amterror.NewAMTError("e:AccessDenied", "x", ""), true},
		{"b:DestinationUnreachable fault", amterror.NewAMTError("b:DestinationUnreachable", "x", ""), true},
		{"wrapped fault", fmt.Errorf("enumerate: %w", amterror.NewAMTError("b:DestinationUnreachable", "x", "")), true},
		{"401", errors.New("wsman.Client post received: 401 Unauthorized\n"), false},
		{"403", errors.New("wsman.Client post received: 403 Forbidden\n"), false},
		{"500 with AccessDenied body", errors.New("wsman.Client post received: 500 Internal Server Error\n<a:Subcode>b:AccessDenied</a:Subcode>"), false},
		{"other fault", amterror.NewAMTError("a:InternalError", "x", ""), false},
		{"wrapped ErrAccessDenied without fault", fmt.Errorf("op: %w", ErrAccessDenied), false},
		{"nil", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := coreClassDenied(tc.err); got != tc.want {
				t.Fatalf("coreClassDenied = %v, want %v", got, tc.want)
			}
		})
	}
}
