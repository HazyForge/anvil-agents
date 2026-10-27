package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"

	"github.com/hazyforge/anvil-agents/internal/onechat/wire"
)

// Commands and hooks use the same hello gate as Desktop. Replies may be mixed
// with broadcasts, so only the response to the current request completes a call.
type daemonRPC struct {
	conn   net.Conn
	reader *bufio.Reader
	stop   func() bool
	nextID int
}

func openDaemonRPC(ctx context.Context, socket, name string) (*daemonRPC, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("connect to onechat: %w; run onechat daemon start", err)
	}
	client := &daemonRPC{conn: conn, reader: bufio.NewReader(conn)}
	client.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			client.Close()
			return nil, err
		}
	}
	raw, err := client.call("hello", map[string]any{"client": map[string]string{"name": name, "kind": "cli"}})
	var hello struct {
		Protocol int
		Host     struct{ ID string }
	}
	if err == nil && (json.Unmarshal(raw, &hello) != nil || hello.Protocol != 1 || hello.Host.ID == "") {
		err = fmt.Errorf("onechat returned an invalid hello response")
	}
	if err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *daemonRPC) Close() {
	c.stop()
	_ = c.conn.Close()
}

func (c *daemonRPC) call(op string, body any) (json.RawMessage, error) {
	c.nextID++
	id := strconv.Itoa(c.nextID)
	raw, err := json.Marshal(map[string]any{"type": "request", "id": id, "op": op, "body": body})
	if err != nil {
		return nil, err
	}
	if err := wire.WriteLine(c.conn, raw); err != nil {
		return nil, err
	}
	for {
		raw, err := wire.ReadLine(c.reader)
		if err != nil {
			return nil, err
		}
		var response struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			OK    bool            `json:"ok"`
			Body  json.RawMessage `json:"body"`
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(raw, &response); err != nil {
			return nil, fmt.Errorf("onechat returned invalid protocol JSON")
		}
		if response.Type == "event" {
			continue
		}
		if response.Type != "response" || response.ID != id {
			return nil, fmt.Errorf("onechat returned an unexpected response")
		}
		if !response.OK {
			return nil, fmt.Errorf("onechat %s: %s", op, response.Error.Message)
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(response.Body, &object) != nil || object == nil {
			return nil, fmt.Errorf("onechat returned an invalid %s body", op)
		}
		return response.Body, nil
	}
}
