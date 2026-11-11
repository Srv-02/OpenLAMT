package amt

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

const (
	pullEnd  = `<a:Envelope xmlns:a="s" xmlns:g="e"><a:Body><g:PullResponse><g:Items/><g:EndOfSequence></g:EndOfSequence></g:PullResponse></a:Body></a:Envelope>`
	pullMore = `<a:Envelope xmlns:a="s" xmlns:g="e"><a:Body><g:PullResponse><g:Items/><g:EnumerationContext>ctx-next</g:EnumerationContext></g:PullResponse></a:Body></a:Envelope>`
)

func TestEnumeratePullSingle(t *testing.T) {
	var pulls []string
	got, err := enumeratePull(
		func() (string, error) { return "ctx-1", nil },
		func(ctx string) ([]string, string, error) {
			pulls = append(pulls, ctx)
			return []string{"a", "b"}, pullEnd, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) || !reflect.DeepEqual(pulls, []string{"ctx-1"}) {
		t.Fatalf("got %v, pulls %v", got, pulls)
	}
}

func TestEnumeratePullTwo(t *testing.T) {
	var pulls []string
	got, err := enumeratePull(
		func() (string, error) { return "ctx-1", nil },
		func(ctx string) ([]string, string, error) {
			pulls = append(pulls, ctx)
			if len(pulls) == 1 {
				return []string{"a"}, pullMore, nil
			}
			return []string{"b", "c"}, pullEnd, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) || !reflect.DeepEqual(pulls, []string{"ctx-1", "ctx-next"}) {
		t.Fatalf("got %v, pulls %v", got, pulls)
	}
}

func TestEnumeratePullEmptyContext(t *testing.T) {
	_, err := enumeratePull(
		func() (string, error) { return "", nil },
		func(string) ([]string, string, error) { t.Fatal("Pull must not run"); return nil, "", nil })
	if !errors.Is(err, ErrIncompleteResponse) {
		t.Fatalf("got %v", err)
	}
}

func TestEnumeratePullNoContextNoEnd(t *testing.T) {
	// Neither EndOfSequence nor a context: treated as the end.
	got, err := enumeratePull(
		func() (string, error) { return "ctx-1", nil },
		func(string) ([]string, string, error) {
			return []string{"a"}, `<a:Envelope xmlns:a="s" xmlns:g="e"><a:Body><g:PullResponse/></a:Body></a:Envelope>`, nil
		})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestEnumeratePullEndless(t *testing.T) {
	n := 0
	_, err := enumeratePull(
		func() (string, error) { return "ctx-1", nil },
		func(string) ([]string, string, error) { n++; return []string{"x"}, pullMore, nil })
	if !errors.Is(err, ErrIncompleteResponse) || !strings.Contains(err.Error(), "did not end") {
		t.Fatalf("got %v", err)
	}
	if n != 16 {
		t.Fatalf("pulls = %d, want 16", n)
	}
}

func TestEnumeratePullErrors(t *testing.T) {
	boom := errors.New("boom")
	if _, err := enumeratePull(func() (string, error) { return "", boom }, func(string) ([]string, string, error) { return nil, "", nil }); !errors.Is(err, boom) {
		t.Fatalf("enumerate: %v", err)
	}
	if _, err := enumeratePull(func() (string, error) { return "c", nil }, func(string) ([]string, string, error) { return nil, "", boom }); !errors.Is(err, boom) {
		t.Fatalf("pull: %v", err)
	}
}
