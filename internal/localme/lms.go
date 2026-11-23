package localme

import (
	"fmt"
	"slices"
)

// rpc-go's CCM activation talks WS-Man to 127.0.0.1:16992 (16993 with TLS) when something
// listens there and assumes it is Intel's LMS; only when nobody listens does it use LME over
// HECI. Any local user can bind those ports while LMS is not running: the elevated helper would
// then send the HostBasedSetupService call (an MD5 digest of admin, a realm the listener
// chooses and the new password, open to an offline attack) to that program, and could be told
// that the activation worked. So the helper checks the listeners right before the activation:
// none, or only the genuine LMS (Linux: a socket of root; Windows: the process of the running
// service "LMS"), and the activation goes on; anything else stops it before the password is
// sent.
//
// A window remains between the check and rpc-go's dial: a program that binds a free port in
// exactly that moment is not seen. The check runs immediately before the activation to keep it
// as short as possible.

// lmsPorts are the ports of LMS on 127.0.0.1: WS-Man over HTTP and over TLS.
var lmsPorts = []int{16992, 16993}

// ForeignLMSError: another program than Intel's LMS listens on an LMS port of this computer;
// the activation did not start and no password was sent.
type ForeignLMSError struct{ Port int }

func (e *ForeignLMSError) Error() string {
	return fmt.Sprintf("another program listens on the Intel AMT port %d", e.Port)
}

// tcpListener is a listening TCP socket that receives connections to 127.0.0.1.
type tcpListener struct {
	Port  int
	Owner uint32 // Linux: the uid of the socket; Windows: the process ID
}

// foreignLMS returns a *ForeignLMSError for the first listener on an LMS port that genuine does
// not accept, nil when there is none.
func foreignLMS(ls []tcpListener, genuine func(tcpListener) bool) error {
	for _, l := range ls {
		if slices.Contains(lmsPorts, l.Port) && !genuine(l) {
			return &ForeignLMSError{Port: l.Port}
		}
	}
	return nil
}

// checkLMS runs right before rpc-go's activation (platformCheckLMS); tests replace it.
var checkLMS = platformCheckLMS
