package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hazyforge/anvil-agents/internal/actoracp"
	"github.com/hazyforge/anvil-agents/internal/substrate"
)

type arguments []string

func (a *arguments) String() string         { return strings.Join(*a, " ") }
func (a *arguments) Set(value string) error { *a = append(*a, value); return nil }

// processTransport closes the pipes and kills/reaps the owned harness when a
// connection fails. Harness stdout is ACP only; stderr is never public output.
type processTransport struct {
	io.Reader
	io.Writer
	fence  *processFence
	cmd    *exec.Cmd
	stdin  io.Closer
	stdout io.Closer
	once   sync.Once
}

func (p *processTransport) Close() error {
	p.once.Do(func() {
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		_ = p.fence.kill()
		_ = p.cmd.Wait()
		p.fence.waitDescendants()
	})
	return nil
}

func startHarness(ctx context.Context, command, workdir string, args []string) (*processTransport, error) {
	if !filepath.IsAbs(command) || !filepath.IsAbs(workdir) {
		return nil, fmt.Errorf("stdio-command and workdir must be absolute paths")
	}
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = workdir
	fence, err := newProcessFence(cmd)
	if err != nil {
		return nil, err
	}
	cmd.WaitDelay = time.Second
	// Fixed operator environment only. HTTP authorization, kubeconfig and
	// cluster tokens cannot reach the child. No auth material is provisioned.
	for _, name := range []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TMPDIR", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME"} {
		if value, ok := os.LookupEnv(name); ok {
			cmd.Env = append(cmd.Env, name+"="+value)
		}
	}
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open harness stdin")
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("open harness stdout")
	}
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start ACP harness failed")
	}
	return &processTransport{Reader: stdout, Writer: stdin, cmd: cmd, fence: fence, stdin: stdin, stdout: stdout}, nil
}

func main() {
	listen := flag.String("listen", ":80", "HTTP listen address")
	command := flag.String("stdio-command", "", "Absolute native ACP executable; empty retains the echo spike")
	workdir := flag.String("workdir", "", "Absolute operator-owned harness working directory")
	turnTimeout := flag.Duration("turn-timeout", 2*time.Minute, "Maximum adapter prompt duration")
	var args arguments
	flag.Var(&args, "stdio-arg", "One fixed native ACP executable argument; repeat as needed")
	flag.Parse()
	log.SetOutput(os.Stderr)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var handler http.Handler = http.HandlerFunc(substrate.ServeACPEcho)
	var ownedClient *actoracp.Client
	if *command != "" {
		transport, err := startHarness(ctx, *command, *workdir, args)
		if err != nil {
			log.Fatal(err)
		}
		client := actoracp.NewClient(transport)
		ownedClient = client
		defer client.Close()
		setupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		adapter, err := actoracp.NewServer(setupCtx, client, actoracp.Config{Workdir: *workdir, TurnTimeout: *turnTimeout})
		cancel()
		if err != nil {
			log.Fatal("ACP harness initialization failed")
		}
		handler = adapter
	} else if len(args) > 0 || *workdir != "" {
		log.Fatal("stdio arguments/workdir require stdio-command")
	}
	server := &http.Server{Addr: *listen, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Printf("anvil-actor-acp listening on %s", *listen)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		if ownedClient != nil {
			_ = ownedClient.Close()
		}
		log.Fatal(err)
	}
}
