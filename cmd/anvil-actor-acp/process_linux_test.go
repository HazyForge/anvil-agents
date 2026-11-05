//go:build linux

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAdapterCloseReapsOwnedToolDescendants(t *testing.T) {
	for _, detached := range []bool{false, true} {
		t.Run(fmt.Sprintf("detached=%v", detached), func(t *testing.T) {
			unrelated := exec.Command("/usr/bin/python3", "-c", "import time; time.sleep(30)")
			if err := unrelated.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = unrelated.Process.Kill(); _ = unrelated.Wait() })
			workdir := t.TempDir()
			marker := filepath.Join(workdir, "after-teardown.txt")
			child := `import os,sys,time
parent=int(sys.argv[1])
while os.getppid()==parent:
 time.sleep(0.005)
with open(sys.argv[2], 'w') as output: output.write('late tool write')
time.sleep(30)
`
			startSession := "False"
			if detached {
				startSession = "True"
			}
			parent := fmt.Sprintf(`import os,subprocess,sys,time
p=subprocess.Popen([sys.executable,'-c',%q,str(os.getpid()),%q],start_new_session=%s,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
print(p.pid,flush=True)
time.sleep(30)
`, child, marker, startSession)
			transport, err := startHarness(context.Background(), "/usr/bin/python3", workdir, []string{"-c", parent})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = transport.Close() })
			line, err := bufio.NewReader(transport).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			childPID, err := strconv.Atoi(strings.TrimSpace(line))
			if err != nil {
				t.Fatal(err)
			}
			if err = transport.Close(); err != nil {
				t.Fatal(err)
			}
			if transport.cmd.ProcessState == nil {
				t.Fatal("harness not reaped")
			}
			if err = syscall.Kill(childPID, 0); err == nil {
				t.Fatal("owned descendant remains after teardown")
			}
			if err = syscall.Kill(unrelated.Process.Pid, 0); err != nil {
				t.Fatal("unrelated child was signalled")
			}
			time.Sleep(100 * time.Millisecond)
			if _, err = os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("descendant wrote after teardown: %v", err)
			}
		})
	}
}

func TestAdapterContextCancellationStopsDescendants(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	script := `import subprocess,sys,time
p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(30)'],start_new_session=True)
print(p.pid,flush=True)
time.sleep(30)
`
	transport, err := startHarness(ctx, "/usr/bin/python3", t.TempDir(), []string{"-c", script})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	line, err := bufio.NewReader(transport).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if err = transport.Close(); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Kill(pid, 0); err == nil {
		t.Fatal("context cancellation left a detached descendant")
	}
}
