package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestNoFyneInDependencies: the headless server must build without Fyne (and so without cgo
// for the GUI), so it can ship as a plain binary.
func TestNoFyneInDependencies(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go list")
	}
	out, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "fyne") {
			t.Errorf("depends on %s", line)
		}
	}
	if !strings.Contains(string(out), "github.com/modelcontextprotocol/go-sdk/mcp") {
		t.Fatalf("unexpected go list output:\n%s", out)
	}
}

// isolate points the user config folder at a temporary one on every platform.
func isolate(t *testing.T) string {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir) // Linux
	t.Setenv("HOME", dir)            // macOS
	t.Setenv("AppData", dir)         // Windows
	return dir
}

func noServe(t *testing.T) serveFunc {
	return func(context.Context, *mcp.Server) error {
		t.Error("serve called")
		return nil
	}
}

// TestRunServesWithDefaultsAndKeepsStdoutClean: without a config file the server starts, the
// action log is created, and nothing but the protocol could reach stdout.
func TestRunServesWithDefaultsAndKeepsStdoutClean(t *testing.T) {
	isolate(t)
	var stdout, stderr bytes.Buffer
	served := false
	code := run(nil, &stdout, &stderr, func(_ context.Context, srv *mcp.Server) error {
		served = srv != nil
		return nil
	})
	if code != 0 || !served || stdout.Len() != 0 {
		t.Fatalf("code=%d served=%v stdout=%q stderr=%q", code, served, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "everything allowed") {
		t.Fatalf("stderr %q", stderr.String())
	}
}

// TestRunRefusesBadConfig: a config with an unknown key or a missing --config file stops the
// server before it serves.
func TestRunRefusesBadConfig(t *testing.T) {
	dir := isolate(t)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"readonly": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"--config", bad}, {"--config", filepath.Join(dir, "missing.json")}, {"extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr, noServe(t)); code == 0 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("%v: code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr, noServe(t)); code != 0 || !strings.HasPrefix(stdout.String(), "openlamt-mcp ") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

// TestReleaseVersionOverridesBuildInfo: the release workflow sets releaseVersion with -ldflags,
// and --version prints it.
func TestReleaseVersionOverridesBuildInfo(t *testing.T) {
	old := releaseVersion
	t.Cleanup(func() { releaseVersion = old })
	releaseVersion = "v9.8.7-rc1"
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr, noServe(t)); code != 0 || stdout.String() != "openlamt-mcp v9.8.7-rc1\n" {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}
