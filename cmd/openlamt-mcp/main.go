// Command openlamt-mcp is an MCP server for AI agents: it offers the AMT functions of OpenLAMT
// for the devices saved as favorites in the app. The MCP client starts it and talks to it over
// stdin and stdout; it opens no network port. Everything else it prints goes to stderr.
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

// run reads flags and config, builds the server and serves it; it returns the exit code.
func run(args []string, stdout, stderr io.Writer, serve serveFunc) int {
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
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "openlamt-mcp: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "openlamt-mcp", version())
		return 0
	}

	favPath, err := favorites.DefaultPath()
	if err != nil {
		fmt.Fprintln(stderr, "openlamt-mcp:", err)
		return 1
	}
	path, explicit := *configPath, *configPath != ""
	if !explicit {
		path = filepath.Join(filepath.Dir(favPath), mcpserver.ConfigFileName)
	}
	cfg, err := mcpserver.Load(path, explicit)
	if err != nil {
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

	keys := keychain.OS{}
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
	logger.Info("openlamt-mcp started", "version", version(), "config", source, "read_only", cfg.ReadOnly,
		"allowed_devices", cfg.AllowedDevices, "client_confirmation", cfg.RequireClientConfirmation, "action_log", logPath)

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
