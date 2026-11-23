package mcpserver

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/session"
)

// Options wires the server.
type Options struct {
	Config    Config
	Favorites FavoriteSource
	Keys      keychain.Store
	Connect   session.ConnectFunc // session.AMTConnect outside tests
	// Logins follows a changed login or certificate pin of a device into the keychain and the
	// favorites, as in the app (*favorites.Manager); nil saves nothing.
	Logins  session.LoginStore
	Log     *ActionLog   // nil: no action log
	Logger  *slog.Logger // diagnostics, stderr; nil discards
	Version string
}

type server struct {
	cfg     Config
	favs    FavoriteSource
	keys    keychain.Store
	connect session.ConnectFunc
	logins  session.LoginStore
	log     *ActionLog
	logger  *slog.Logger
	kvm     *kvmManager
	sol     *solManager
	media   *mediaManager
	// mountAsks holds what open media_mount questions showed.
	mountAsks mountAsks
	// stateKey keys the request state that binds a settings or account question to its call.
	stateKey []byte
}

// Server is the MCP server with the device sessions it keeps across tool calls. Close it when
// the client has gone.
type Server struct {
	*mcp.Server
	s *server
}

// Close ends every KVM session (held keys and buttons are released first), every serial
// console session and every mount (a mount in progress is cancelled; boot options media_boot_next
// set are cleared), and refuses new ones. It is safe to call more than once.
func (s *Server) Close() {
	var wg sync.WaitGroup
	wg.Go(s.s.kvm.closeAll)
	wg.Go(s.s.sol.closeAll)
	wg.Go(s.s.media.closeAll)
	wg.Wait()
}

const instructions = `openlamt-mcp controls Intel AMT devices saved as favorites in the OpenLAMT app.
Call list_devices first; every other tool takes the favorite name as "device".
Device passwords are never needed: they come from the OS keychain.
Power actions act on real hardware: check device_status before and after.
boot_to_bios, kvm_connect, sol_connect and media_mount may need user consent on the device: then ask a person at the device for the six-digit code on its screen and pass it as consent_code.
For the screen: kvm_connect once, then kvm_screenshot, kvm_send_keys, kvm_mouse and kvm_type_text as often as needed; kvm_disconnect when done.
kvm_mouse takes x and y in the pixels of the last kvm_screenshot. After input, take a new screenshot (wait_ms helps when the screen changes slowly).
For the serial console: sol_connect once, then sol_read (it goes on where the last read ended; wait_ms waits for new output), sol_type_text and sol_send_keys; sol_disconnect when done.
For media: media_mount an absolute path of an .iso file or a folder on this computer, then media_boot_next (boot from it at the next start) or boot_from_iso (restart from it now); media_status shows the mount; media_unmount when done.
For AMT settings and accounts: get_settings and list_users read them. Changes the OpenLAMT app guards with an orange or red warning always ask the person in the MCP client; red ones (they can lock OpenLAMT out of the device) also need confirm set to the device name. Passwords for new or changed accounts are tool arguments: never repeat them in your answers.`

// New builds the MCP server with the tools the config allows.
func New(o Options) *Server {
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	s := &server{cfg: o.Config, favs: o.Favorites, keys: o.Keys, connect: o.Connect, logins: o.Logins, log: o.Log, logger: logger,
		stateKey: make([]byte, 32)}
	_, _ = rand.Read(s.stateKey) // never fails (crypto/rand)
	s.kvm = newKVMManager(o.Config.KVMIdleTimeout, s.openKVM)
	s.sol = newSOLManager(o.Config.SOLIdleTimeout, s.openSOL)
	s.media = newMediaManager(s.openIDER, s.clearISOBoot, logger)
	srv := mcp.NewServer(&mcp.Implementation{Name: "openlamt-mcp", Title: "OpenLAMT", Version: o.Version},
		&mcp.ServerOptions{Instructions: instructions, Logger: logger})
	s.register(srv)
	return &Server{Server: srv, s: s}
}

// record is what the action log keeps of one call.
type record struct {
	device, action string
}

// handler is a tool handler that fills rec for the action log.
type handler[In, Out any] func(ctx context.Context, req *mcp.CallToolRequest, in In, rec *record) (*mcp.CallToolResult, Out, error)

// addTool registers h with action logging and the server's own error texts.
func addTool[In, Out any](s *server, srv *mcp.Server, t *mcp.Tool, h handler[In, Out]) {
	mcp.AddTool(srv, t, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var rec record
		res, out, err := h(ctx, req, in, &rec)
		var cne *consentNeededError
		if errors.Is(err, errConsentCodeNeeded) && !errors.As(err, &cne) {
			err = &consentNeededError{tool: t.Name} // call this tool again with the code
		}
		e := ActionEntry{Time: time.Now().UTC(), Tool: t.Name, Device: rec.device, Action: rec.action, Outcome: "ok"}
		switch {
		case err != nil && isRefusal(err):
			e.Outcome, e.Message = "refused", explain(err)
		case err != nil:
			e.Outcome, e.Message = "error", explain(err)
			s.logger.Warn("tool failed", "tool", t.Name, "device", rec.device, "err", err)
		case res != nil && res.InputRequests != nil:
			e.Outcome = "confirmation_requested"
		}
		if s.log != nil {
			if lerr := s.log.Write(e); lerr != nil {
				s.logger.Error("writing the action log", "err", lerr)
			}
		}
		if err != nil {
			var zero Out
			return nil, zero, &refusal{msg: e.Message}
		}
		return res, out, nil
	})
}

func boolPtr(b bool) *bool { return &b }

func (s *server) register(srv *mcp.Server) {
	closed := boolPtr(false)
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: closed}
	// list_devices contacts no device and is needed to use any other tool: always offered.
	addTool(s, srv, &mcp.Tool{Name: "list_devices", Annotations: readOnly,
		Description: "List the OpenLAMT favorites this server may use, with host, user name, TLS, whether the certificate is pinned and whether a password is stored. Contacts no device."},
		s.listDevices)
	if s.cfg.Enabled(GroupRead) {
		addTool(s, srv, &mcp.Tool{Name: "device_status", Annotations: readOnly,
			Description: "Read power state, AMT version, control mode (ACM or CCM), TLS and graceful shutdown support of a device."},
			s.deviceStatus)
		addTool(s, srv, &mcp.Tool{Name: "hardware_info", Annotations: readOnly,
			Description: "Read the hardware inventory of a device: system, board, BIOS, CPUs, memory, drives and AMT version."},
			s.hardwareInfo)
		addTool(s, srv, &mcp.Tool{Name: "event_log", Annotations: readOnly, InputSchema: eventLogSchema(),
			Description: "Read the AMT event log of a device (hardware and firmware events), newest first, filtered by severity, time and text."},
			s.eventLog)
		addTool(s, srv, &mcp.Tool{Name: "audit_log", Annotations: readOnly,
			Description: "Read the AMT audit log of a device (who did what in AMT), newest first, filtered by time and text. Needs the audit log permission."},
			s.auditLog)
	}
	if s.cfg.Enabled(GroupPowerBoot) {
		destructive := &mcp.ToolAnnotations{DestructiveHint: boolPtr(true), OpenWorldHint: closed}
		addTool(s, srv, &mcp.Tool{Name: "power_action", Annotations: destructive, InputSchema: powerActionSchema(),
			Description: "Power a device on, off (hard), reset (hard), power cycle (hard) or shut down the operating system gracefully. " +
				"A device that is on refuses on; a device that is off takes only on. shutdown needs the device to offer it (see device_status). " +
				"Everything but on needs the tool group \"destructive\"."},
			s.powerAction)
		addTool(s, srv, &mcp.Tool{Name: "boot_to_bios", Annotations: destructive,
			Description: "Restart a running device into BIOS setup (a hard reset) or power on a device that is off into BIOS setup. " +
				"Devices that require user consent show a six-digit code on their screen: call without consent_code first, ask a person at the device for the code, then call again with it."},
			s.bootToBIOS)
	}
	s.registerKVM(srv)
	s.registerSOL(srv)
	s.registerMedia(srv)
	s.registerSettings(srv)
	s.registerUsers(srv)
}
