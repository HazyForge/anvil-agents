package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/hazyforge/anvil-agents/internal/runnercapabilities"
)

const (
	exitUsage     = 2
	exitAcquire   = 40
	exitVerify    = 41
	exitMCP       = 42
	defaultRunTTL = 5 * time.Minute
)

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr)
	if code != 0 {
		os.Exit(code)
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] + " " + args[1] {
	case "tools install":
		if err := runToolInstall(args[2:], stdout); err != nil {
			fmt.Fprintf(stderr, "tool acquisition failed: %v\n", err)
			return exitAcquire
		}
		return 0
	case "tools verify":
		if err := runToolVerify(args[2:], stdout); err != nil {
			fmt.Fprintf(stderr, "tool verification failed: %v\n", err)
			return exitVerify
		}
		return 0
	case "mcp preflight":
		if err := runMCPPreflight(args[2:], stdout); err != nil {
			fmt.Fprintf(stderr, "MCP preflight failed: %v\n", err)
			return exitMCP
		}
		return 0
	case "mcp configure":
		if err := runMCPConfigure(args[2:], stdout); err != nil {
			fmt.Fprintf(stderr, "MCP native configuration failed: %v\n", err)
			return exitMCP
		}
		return 0
	default:
		usage(stderr)
		return exitUsage
	}
}

func runMCPConfigure(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("mcp configure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "normalized MCP manifest")
	backend := flags.String("backend", "", "runner backend")
	configPath := flags.String("config", "", "native backend config path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid MCP configure arguments")
	}
	contents, err := readBoundedFile(*manifestPath, 2<<20)
	if err != nil {
		return err
	}
	manifest, err := runnercapabilities.ParseMCPManifest(contents)
	if err != nil {
		return err
	}
	if err := runnercapabilities.ConfigureNativeMCP(*backend, *configPath, manifest); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "ANVIL_AGENT_MCP_CONFIGURED backend=%s servers=%d\n", *backend, len(manifest))
	return nil
}

func runToolInstall(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("tools install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "normalized tool manifest")
	cacheRoot := flags.String("cache-root", "", "content-addressed cache root")
	binDir := flags.String("bin-dir", "", "per-run executable directory")
	osName := flags.String("os", runtime.GOOS, "runner operating system")
	arch := flags.String("arch", runtime.GOARCH, "runner architecture")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid tools install arguments")
	}
	manifest, err := readToolManifest(*manifestPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRunTTL)
	defer cancel()
	client := &http.Client{
		Timeout: defaultRunTTL,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 5 || request.URL.Scheme != "https" || request.URL.User != nil {
				return errors.New("artifact redirect was rejected")
			}
			return nil
		},
	}
	installed, err := runnercapabilities.InstallTools(ctx, manifest, runnercapabilities.InstallOptions{
		CacheRoot:  *cacheRoot,
		BinDir:     *binDir,
		Platform:   runnercapabilities.Platform{OS: *osName, Arch: *arch},
		HTTPClient: client,
	})
	if err != nil {
		return err
	}
	for _, tool := range installed {
		state := "installed"
		if tool.Reused {
			state = "cached"
		}
		fmt.Fprintf(stdout, "ANVIL_AGENT_TOOL_READY name=%s executable=%s state=%s\n", tool.Name, tool.ExecutableName, state)
	}
	return nil
}

func runToolVerify(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("tools verify", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "normalized tool manifest")
	binDir := flags.String("bin-dir", "", "per-run executable directory")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid tools verify arguments")
	}
	manifest, err := readToolManifest(*manifestPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultRunTTL)
	defer cancel()
	if err := runnercapabilities.VerifyTools(ctx, manifest, runnercapabilities.VerifyOptions{BinDir: *binDir}); err != nil {
		return err
	}
	for _, tool := range manifest {
		if len(tool.VerifyCommand) > 0 {
			fmt.Fprintf(stdout, "ANVIL_AGENT_TOOL_VERIFIED name=%s\n", tool.Name)
		}
	}
	return nil
}

func runMCPPreflight(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("mcp preflight", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	manifestPath := flags.String("manifest", "", "normalized MCP manifest")
	backend := flags.String("backend", "", "runner backend")
	timeout := flags.Duration("timeout", 20*time.Second, "per-server timeout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid MCP preflight arguments")
	}
	contents, err := readBoundedFile(*manifestPath, 2<<20)
	if err != nil {
		return err
	}
	manifest, err := runnercapabilities.ParseMCPManifest(contents)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(len(manifest)+1)**timeout)
	defer cancel()
	results, err := runnercapabilities.PreflightMCP(ctx, *backend, manifest, runnercapabilities.MCPPreflightOptions{Timeout: *timeout})
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Fprintf(stdout, "ANVIL_AGENT_MCP_READY name=%s transport=%s tools=%d\n", result.Name, result.Transport, result.ToolCount)
	}
	return nil
}

func readToolManifest(path string) (runnercapabilities.ToolManifest, error) {
	contents, err := readBoundedFile(path, runnercapabilities.DefaultMaxToolManifestSize)
	if err != nil {
		return nil, err
	}
	return runnercapabilities.ParseToolManifest(contents)
}

func readBoundedFile(path string, maxBytes int64) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("manifest path is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("open manifest")
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, errors.New("read manifest")
	}
	if int64(len(contents)) > maxBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxBytes)
	}
	return contents, nil
}

func usage(writer io.Writer) {
	fmt.Fprintln(writer, "Usage:")
	fmt.Fprintln(writer, "  anvil-agent-capabilities tools install --manifest FILE --cache-root DIR --bin-dir DIR")
	fmt.Fprintln(writer, "  anvil-agent-capabilities tools verify --manifest FILE --bin-dir DIR")
	fmt.Fprintln(writer, "  anvil-agent-capabilities mcp preflight --manifest FILE --backend BACKEND")
	fmt.Fprintln(writer, "  anvil-agent-capabilities mcp configure --manifest FILE --backend BACKEND --config FILE")
}
