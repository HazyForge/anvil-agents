// Package actoracp bridges Anvil's actor HTTP surface to a native ACP stdio
// connection. It does not provide model credentials or client tool authority.
package actoracp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"
)

const maxFrameBytes = 1 << 20

var (
	ErrClosed   = errors.New("ACP harness connection closed")
	ErrProtocol = errors.New("invalid ACP harness response")
	ErrHarness  = errors.New("ACP harness request failed")
	ErrBusy     = errors.New("ACP session busy")
)

type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type frame struct {
	message message
	err     error
}

type writeRequest struct {
	data  []byte
	done  chan error
	ctx   context.Context
	check func() error
}

type pendingCall struct {
	events  chan frame
	method  string
	session string
}

// Client owns one native ACP connection. Close must unblock transport IO.
// It never retries a prompt or starts a replacement harness after failure.
type Client struct {
	transport      io.ReadWriteCloser
	mu             sync.Mutex
	seq            uint64
	pending        map[string]pendingCall
	streams        map[string]chan frame
	closed         chan struct{}
	once           sync.Once
	writes         chan writeRequest
	catalogLimit   int
	catalogs       map[string]catalogSnapshot
	catalogChanged chan struct{}
}

func NewClient(transport io.ReadWriteCloser) *Client {
	c := &Client{transport: transport, pending: map[string]pendingCall{}, streams: map[string]chan frame{}, closed: make(chan struct{}), writes: make(chan writeRequest, 8), catalogChanged: make(chan struct{}, 1)}
	go c.readLoop()
	go c.writeLoop()
	return c
}

func (c *Client) Close() error {
	c.once.Do(func() {
		_ = c.transport.Close()
		close(c.closed)
	})
	return nil
}

func (c *Client) ready() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case <-c.closed:
			return
		case request := <-c.writes:
			// Check the latest observed selection and cancellation at the
			// actual write boundary, not merely while preparing the RPC.
			checkErr := request.ctx.Err()
			if checkErr == nil && request.check != nil {
				checkErr = request.check()
			}
			if checkErr != nil {
				request.done <- checkErr
				continue
			}
			data := request.data
			var err error
			for len(data) > 0 {
				var n int
				n, err = c.transport.Write(data)
				if err != nil {
					break
				}
				if n <= 0 {
					err = io.ErrShortWrite
					break
				}
				data = data[n:]
			}
			if err != nil {
				_ = c.Close()
				err = ErrClosed
			}
			request.done <- err
		}
	}
}

func (c *Client) send(ctx context.Context, value any) error {
	return c.sendChecked(ctx, value, nil)
}

func (c *Client) sendChecked(ctx context.Context, value any, check func() error) error {
	data, err := json.Marshal(value)
	if err != nil || len(data) > maxFrameBytes {
		return ErrProtocol
	}
	request := writeRequest{data: append(data, '\n'), done: make(chan error, 1), ctx: ctx, check: check}
	select {
	case <-c.closed:
		return ErrClosed
	case <-ctx.Done():
		return ctx.Err()
	case c.writes <- request:
	}
	select {
	case <-c.closed:
		return ErrClosed
	case <-ctx.Done():
		// A partial write cannot be safely recovered or replayed.
		_ = c.Close()
		return ctx.Err()
	case err := <-request.done:
		return err
	}
}

func (c *Client) notify(ctx context.Context, method string, params any) error {
	return c.send(ctx, map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (c *Client) call(ctx context.Context, method string, params any, session string, update func(json.RawMessage) error, sent func() error) (json.RawMessage, error) {
	return c.callChecked(ctx, method, params, session, update, sent, nil)
}

func (c *Client) callChecked(ctx context.Context, method string, params any, session string, update func(json.RawMessage) error, sent func() error, check func() error) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	events := make(chan frame, 64)
	c.mu.Lock()
	if session != "" && c.streams[session] != nil {
		c.mu.Unlock()
		return nil, ErrBusy
	}
	c.seq++
	id := strconv.FormatUint(c.seq, 10)
	c.pending[id] = pendingCall{events: events, method: method, session: session}
	if session != "" {
		c.streams[session] = events
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		if session != "" {
			delete(c.streams, session)
		}
		c.mu.Unlock()
	}()
	if err := c.sendChecked(ctx, map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params}, check); err != nil {
		if !errors.Is(err, ErrClosed) || len(events) == 0 {
			return nil, err
		}
	} else if sent != nil {
		if err := sent(); err != nil {
			_ = c.Close()
			return nil, err
		}
	}
	for {
		var event frame
		select {
		case <-c.closed:
			// EOF can follow a valid result immediately. Consume already
			// correlated frames in order before declaring an incomplete turn.
			select {
			case event = <-events:
			default:
				return nil, ErrClosed
			}
		case <-ctx.Done():
			// Disconnect/timeouts cannot prove cancellation completed. Close the
			// connection rather than allowing another turn to overlap it.
			_ = c.Close()
			return nil, ctx.Err()
		case event = <-events:
		}
		if event.err != nil {
			return nil, event.err
		}
		if event.message.Method == "session/update" {
			if update != nil {
				if err := update(event.message.Params); err != nil {
					_ = c.Close()
					return nil, err
				}
			}
			continue
		}
		if event.message.Error != nil {
			return nil, ErrHarness
		}
		return event.message.Result, nil
	}
}

func (c *Client) readLoop() {
	defer c.Close()
	scanner := bufio.NewScanner(c.transport)
	scanner.Buffer(make([]byte, 4096), maxFrameBytes)
	for scanner.Scan() {
		var msg message
		if json.Unmarshal(scanner.Bytes(), &msg) != nil || msg.JSONRPC != "2.0" {
			return
		}
		if msg.Method != "" && len(msg.ID) > 0 {
			// No approval, filesystem, terminal, authentication or MCP broker.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			var response any = map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": rpcError{Code: -32601, Message: "client method unavailable"}}
			if msg.Method == "session/request_permission" {
				response = map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}}
			}
			err := c.send(ctx, response)
			cancel()
			if err != nil {
				return
			}
			continue
		}
		c.mu.Lock()
		var events chan frame
		var pending pendingCall
		if msg.Method == "session/update" {
			var params struct {
				SessionID string `json:"sessionId"`
			}
			if json.Unmarshal(msg.Params, &params) != nil {
				c.mu.Unlock()
				return
			}
			events = c.streams[params.SessionID]
		} else if msg.Method == "" && len(msg.ID) > 0 {
			pending = c.pending[string(msg.ID)]
			events = pending.events
			if (msg.Error == nil) == (len(msg.Result) == 0) {
				c.mu.Unlock()
				return
			}
		}
		if !c.recordCatalog(msg, pending) {
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()
		if events != nil {
			// Preserve update/result ordering; overflow fails the connection
			// instead of silently dropping output or blocking all sessions.
			select {
			case events <- frame{message: msg}:
			default:
				return
			}
		}
	}
}
