package host

import (
	"bufio"
	"net"
	"time"

	"github.com/hazyforge/anvil-agents/internal/onechat/wire"
)

// Both transports share admission, queues and turn replay. The opcode lets the
// protocol reject binary WebSocket messages without a separate request path.
type transport interface {
	ReadMessage() (int, []byte, error)
	WriteText([]byte) error
	Abort() error
}

type lineConn struct {
	conn   net.Conn
	reader *bufio.Reader
}

func (c *lineConn) ReadMessage() (int, []byte, error) {
	raw, err := wire.ReadLine(c.reader)
	return 1, raw, err
}

func (c *lineConn) WriteText(raw []byte) error {
	if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	return wire.WriteLine(c.conn, raw)
}

func (c *lineConn) Abort() error { return c.conn.Close() }

func (h *Host) acceptClient(conn transport) {
	c := &client{host: h, conn: conn, out: make(chan []byte, queueSize), done: make(chan struct{})}
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		c.close()
		return
	}
	h.clients[c] = struct{}{}
	h.workers.Add(2)
	h.mu.Unlock()
	go c.writeLoop()
	go c.readLoop()
}

func (h *Host) serveControl(listener net.Listener) error {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		if err := checkPeer(conn); err != nil {
			_ = conn.Close()
			continue
		}
		h.acceptClient(&lineConn{conn: conn, reader: bufio.NewReader(conn)})
	}
}
