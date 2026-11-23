//go:build windows

package localme

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tcpTables is what the LMS check asks Windows; tests replace it.
type tcpTables interface {
	// listeners are the TCP listeners on 127.0.0.1 or the wildcard, IPv4 and IPv6, with their
	// owning process.
	listeners() ([]tcpListener, error)
	// lmsPID is the process of the service "LMS" while it runs; 0 when it is not installed or
	// does not run. Only an administrator can install a service of that name.
	lmsPID() (uint32, error)
}

func platformCheckLMS() error { return checkLMSWith(winTCP{}) }

// checkLMSWith accepts only listeners of the running LMS service.
func checkLMSWith(t tcpTables) error {
	ls, err := t.listeners()
	if err != nil {
		return fmt.Errorf("checking the LMS ports: %w", err)
	}
	pid, err := t.lmsPID()
	if err != nil {
		return fmt.Errorf("checking the LMS service: %w", err)
	}
	return foreignLMS(ls, func(l tcpListener) bool { return pid != 0 && l.Owner == pid })
}

// winTCP is tcpTables over iphlpapi and the service manager.
type winTCP struct{}

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

const (
	tcpTableOwnerPIDListener = 3 // TCP_TABLE_OWNER_PID_LISTENER
	afInet6                  = 23
)

// mibTCPRowOwnerPID is MIB_TCPROW_OWNER_PID.
type mibTCPRowOwnerPID struct {
	State, LocalAddr, LocalPort, RemoteAddr, RemotePort, OwningPID uint32
}

// mibTCP6RowOwnerPID is MIB_TCP6ROW_OWNER_PID.
type mibTCP6RowOwnerPID struct {
	LocalAddr                 [16]byte
	LocalScopeID, LocalPort   uint32
	RemoteAddr                [16]byte
	RemoteScopeID, RemotePort uint32
	State, OwningPID          uint32
}

// extendedTCPTable returns the raw listener table of family.
func extendedTCPTable(family uint32) ([]byte, error) {
	size := uint32(4096)
	for range 8 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(family), tcpTableOwnerPIDListener, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			continue
		default:
			return nil, windows.Errno(r)
		}
	}
	return nil, errors.New("the TCP table keeps growing")
}

// port is a port of the tables: network byte order in the low 16 bits.
func port(p uint32) int { return int(p&0xff)<<8 | int(p>>8&0xff) }

func (winTCP) listeners() ([]tcpListener, error) {
	var out []tcpListener
	b, err := extendedTCPTable(windows.AF_INET)
	if err != nil {
		return nil, err
	}
	if len(b) >= 4 {
		n := *(*uint32)(unsafe.Pointer(&b[0]))
		rows := unsafe.Slice((*mibTCPRowOwnerPID)(unsafe.Pointer(&b[4])), n)
		for _, r := range rows {
			// 127.0.0.1 (in memory order) and 0.0.0.0
			if r.LocalAddr == 0x0100007F || r.LocalAddr == 0 {
				out = append(out, tcpListener{Port: port(r.LocalPort), Owner: r.OwningPID})
			}
		}
	}
	b, err = extendedTCPTable(afInet6)
	if err != nil {
		return out, nil // no IPv6 stack: only the IPv4 listeners count
	}
	if len(b) >= 4 {
		n := *(*uint32)(unsafe.Pointer(&b[0]))
		rows := unsafe.Slice((*mibTCP6RowOwnerPID)(unsafe.Pointer(&b[4])), n)
		mapped := [16]byte{10: 0xff, 11: 0xff, 12: 127, 15: 1}
		for _, r := range rows {
			if r.LocalAddr == [16]byte{} || r.LocalAddr == mapped {
				out = append(out, tcpListener{Port: port(r.LocalPort), Owner: r.OwningPID})
			}
		}
	}
	return out, nil
}

func (winTCP) lmsPID() (uint32, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(m)
	name, _ := windows.UTF16PtrFromString("LMS")
	s, err := windows.OpenService(m, name, windows.SERVICE_QUERY_STATUS)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(s)
	var st windows.SERVICE_STATUS_PROCESS
	var need uint32
	if err := windows.QueryServiceStatusEx(s, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&st)),
		uint32(unsafe.Sizeof(st)), &need); err != nil {
		return 0, err
	}
	if st.CurrentState != windows.SERVICE_RUNNING {
		return 0, nil
	}
	return st.ProcessId, nil
}
