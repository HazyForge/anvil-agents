package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat/wire"
)

func probeDaemon(ctx context.Context, socket, hostID string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	reader := bufio.NewReader(conn)
	for _, op := range []string{"hello", "status"} {
		body := map[string]any{}
		if op == "hello" {
			body["client"] = map[string]string{"name": "onechat daemon status", "kind": "cli"}
		}
		raw, err := json.Marshal(map[string]any{"type": "request", "id": op, "op": op, "body": body})
		if err != nil {
			return err
		}
		if err := wire.WriteLine(conn, raw); err != nil {
			return err
		}
		for {
			raw, err := wire.ReadLine(reader)
			if err != nil {
				return err
			}
			var response struct {
				Type, ID string
				OK       bool
				Body     json.RawMessage
				Error    struct{ Message string }
			}
			if err := json.Unmarshal(raw, &response); err != nil {
				return err
			}
			if response.Type != "response" || response.ID != op {
				continue
			}
			if !response.OK {
				return fmt.Errorf("daemon %s: %s", op, response.Error.Message)
			}
			if op == "hello" {
				var hello struct {
					Protocol int
					Host     struct{ ID string }
				}
				if json.Unmarshal(response.Body, &hello) != nil || hello.Protocol != 1 || hostID == "" || hello.Host.ID != hostID {
					return fmt.Errorf("socket host identity does not match host.json")
				}
			} else {
				var status struct{ State string }
				if json.Unmarshal(response.Body, &status) != nil || (status.State != "idle" && status.State != "busy") {
					return fmt.Errorf("socket returned an invalid status")
				}
			}
			break
		}
	}
	return nil
}

func proxyDaemon(ctx context.Context, socket string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, err := (&net.Dialer{}).DialContext(dialCtx, "unix", socket)
	cancel()
	if err != nil {
		return fmt.Errorf("connect to onechat at %s: %w; run onechat daemon start", socket, err)
	}
	defer conn.Close()
	done := make(chan error, 2)
	copyLines := func(dst io.Writer, src io.Reader) {
		reader := bufio.NewReader(src)
		for {
			raw, err := wire.ReadLine(reader)
			if err == nil {
				err = wire.WriteLine(dst, raw)
			}
			if err != nil {
				done <- err
				return
			}
		}
	}
	go copyLines(conn, os.Stdin)
	go copyLines(os.Stdout, conn)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
}
