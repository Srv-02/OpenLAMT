package amt

import (
	"encoding/xml"
	"fmt"
)

// maxPulls bounds the Pull repetitions of one enumeration.
const maxPulls = 16

// enumeratePull runs Enumerate and repeats Pull until the response has EndOfSequence or no
// EnumerationContext, at most maxPulls (16) times, and concatenates the items of all pulls.
// An empty context from Enumerate yields ErrIncompleteResponse without a Pull.
func enumeratePull[T any](enum func() (ctx string, err error), pull func(ctx string) (items []T, xmlOutput string, err error)) ([]T, error) {
	ctx, err := enum()
	if err != nil {
		return nil, err
	}
	if ctx == "" {
		return nil, fmt.Errorf("%w: device returned no enumeration context", ErrIncompleteResponse)
	}
	var all []T
	for range maxPulls {
		items, out, err := pull(ctx)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)

		var cont struct {
			Ctx string    `xml:"Body>PullResponse>EnumerationContext"`
			End *struct{} `xml:"Body>PullResponse>EndOfSequence"`
		}
		if err := xml.Unmarshal([]byte(out), &cont); err != nil {
			return nil, fmt.Errorf("%w: unreadable pull response: %v", ErrIncompleteResponse, err)
		}
		if cont.End != nil || cont.Ctx == "" {
			return all, nil
		}
		ctx = cont.Ctx
	}
	return nil, fmt.Errorf("%w: enumeration did not end", ErrIncompleteResponse)
}
