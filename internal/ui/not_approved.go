package ui

import "log"

// notApprovedHook, if set, sees every call of notApproved (tests).
var notApprovedHook func(what string)

// notApproved stands for a dialog or text that has no approved design yet: the action is not
// offered and nothing is shown, the call is only logged. The caller puts its control back.
// TODO(design): replace every call once the design for it exists.
func notApproved(what string) {
	log.Printf("ui: not in the approved design yet: %s", what)
	if notApprovedHook != nil {
		notApprovedHook(what)
	}
}
