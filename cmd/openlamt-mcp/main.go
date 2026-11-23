// Command openlamt-mcp is an MCP server for AI agents: it offers the AMT functions of OpenLAMT
// for the devices saved as favorites in the app. The MCP client starts it and talks to it over
// stdin and stdout; it opens no network port. Everything else it prints goes to stderr.
//
// Two subcommands are for a person at a terminal: "openlamt-mcp password <favorite>" stores the
// password of a favorite in the system keychain, "openlamt-mcp trust <favorite>" pins the TLS
// certificate of a favorite (in the system keychain, or with certificates.pin_file in
// openlamt-pins.json) after the person checked its fingerprint.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Srv-02/openlamt/internal/favorites"
	"github.com/Srv-02/openlamt/internal/keychain"
	"github.com/Srv-02/openlamt/internal/mcpserver"
	"github.com/Srv-02/openlamt/internal/session"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, func(ctx context.Context, srv *mcp.Server) error {
		return srv.Run(ctx, &mcp.StdioTransport{})
	}))
}

// releaseVersion is the release tag, set by the release workflow with
// -ldflags "-X main.releaseVersion=<tag>"; empty for other builds.
var releaseVersion string

// version is the release tag, else the module version of the build, "dev" for a local build.
func version() string {
	if releaseVersion != "" {
		return releaseVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// serveFunc runs the built server until the client disconnects (stdio in main, a stub in tests).
type serveFunc func(context.Context, *mcp.Server) error

// run reads flags and config, builds the server and serves it, or runs a subcommand; it returns
// the exit code.
func run(args []string, stdout, stderr io.Writer, serve serveFunc) int {
	return runWith(args, stdout, stderr, serve, defaultDeps(stderr))
}

// configPaths returns the favorites file and the config file: configFlag, else the default
// next to favorites.json. explicit is true for a file given with --config.
func configPaths(configFlag string) (favPath, path string, explicit bool, err error) {
	if favPath, err = favorites.DefaultPath(); err != nil {
		return "", "", false, err
	}
	path, explicit = configFlag, configFlag != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(favPath), mcpserver.ConfigFileName)
	}
	return favPath, path, explicit, nil
}

func runWith(args []string, stdout, stderr io.Writer, serve serveFunc, d deps) int {
	// Nothing may write to stdout but the protocol: route the standard logger to stderr too.
	log.SetOutput(stderr)
	logger := slog.New(slog.NewTextHandler(stderr, nil))

	fs := flag.NewFlagSet("openlamt-mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "config file (default: "+mcpserver.ConfigFileName+" in the OpenLAMT config folder)")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "openlamt-mcp", version())
		return 0
	}
	if fs.NArg() > 0 {
		switch fs.Arg(0) {
		case "password", "trust":
			return subcommand(fs.Arg(0), fs.Args()[1:], *configPath, stdout, stderr, d)
		}
		fmt.Fprintf(stderr, "openlamt-mcp: unexpected argument %q (subcommands: password, trust)\n", fs.Arg(0))
		return 2
	}

	favPath, path, explicit, err := configPaths(*configPath)
	if err != nil {
		fmt.Fprintln(stderr, "openlamt-mcp:", err)
		return 1
	}
	cfg, err := mcpserver.Load(path, explicit)
	if err != nil {
		fmt.Fprintln(stderr, "openlamt-mcp:", err)
		return 1
	}
	if err := cfg.Passwords.Check(); err != nil {
		fmt.Fprintln(stderr, "openlamt-mcp:", err)
		return 1
	}
	var actions *mcpserver.ActionLog
	if cfg.ActionLog {
		if actions, err = mcpserver.OpenActionLog(cfg.ActionLogPath); err != nil {
			fmt.Fprintln(stderr, "openlamt-mcp:", err)
			return 1
		}
	}

	keys, pinPath, err := pinStore(cfg, favPath, d.keys)
	if err != nil {
		fmt.Fprintln(stderr, "openlamt-mcp:", err)
		return 1
	}
	favs := favorites.NewManager(favorites.NewStore(favPath), keys)
	srv := mcpserver.New(mcpserver.Options{
		Config:    cfg,
		Favorites: favs,
		Keys:      keys,
		Logins:    favs, // a changed login or certificate pin follows into keychain and favorites, as in the app
		Connect:   session.AMTConnect,
		Log:       actions,
		Logger:    logger,
		Version:   version(),
	})
	source := cfg.Source
	if source == "" {
		source = "none (defaults: everything allowed)"
	}
	logPath := "off"
	if actions != nil {
		logPath = actions.Path()
	}
	pinFile := "off"
	if pinPath != "" {
		pinFile = pinPath
	}
	passwordCommand := "off"
	if len(cfg.Passwords.Command) > 0 {
		passwordCommand = cfg.Passwords.Command[0]
	}
	logger.Info("openlamt-mcp started", "version", version(), "config", source, "read_only", cfg.ReadOnly,
		"allowed_devices", cfg.AllowedDevices, "client_confirmation", cfg.RequireClientConfirmation, "action_log", logPath,
		"pin_file", pinFile,
		"password_command", passwordCommand, "plaintext_passwords", cfg.Passwords.AllowPlaintext)
	if cfg.Passwords.AllowPlaintext {
		logger.Warn(mcpserver.PlaintextWarning, "env_file", cfg.Passwords.EnvFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// The client disconnected or a signal came: end the KVM sessions cleanly, so the device's
	// single KVM slot is free again and no key stays pressed on the host.
	defer srv.Close()
	if err := serve(ctx, srv.Server); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("openlamt-mcp stopped", "err", err)
		return 1
	}
	return 0
}

// pinStore returns the store of trusted fingerprints and passwords: without certificates.pin_file
// the keychain, with it a file next to favorites.json (and its path). It never switches by itself.
func pinStore(cfg mcpserver.Config, favPath string, keys keychain.Store) (store keychain.Store, pinPath string, err error) {
	if !cfg.Certificates.PinFile {
		return keys, "", nil
	}
	pinPath = filepath.Join(filepath.Dir(favPath), mcpserver.PinFileName)
	store, err = mcpserver.OpenPinFile(pinPath, keys)
	if err != nil {
		return nil, "", err
	}
	return store, pinPath, nil
}
