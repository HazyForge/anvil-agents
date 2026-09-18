package standing

import (
	"bufio"
	"crypto/sha1" // #nosec G505 -- RFC 6455 Section 1.3 mandates SHA-1 for the handshake accept key; not a security use.
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// wsGUID is the fixed RFC 6455 handshake GUID.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// maxWSMessageBytes bounds one reassembled WebSocket message so a misbehaving
// peer cannot grow server memory without limit. Token events are small JSON
// documents; 1MiB matches the stream line budget used elsewhere.
const maxWSMessageBytes = 1 << 20

// wsWriteTimeout bounds one frame write so a stalled reader cannot keep a
// standing session or output-copy goroutine alive.
const wsWriteTimeout = 10 * time.Second

// Frame opcodes the server needs.
const (
	wsOpContinuation = 0x0
	wsOpText         = 0x1
	wsOpBinary       = 0x2
	wsOpClose        = 0x8
	wsOpPing         = 0x9
	wsOpPong         = 0xA
)

// Upgrade validates an RFC 6455 client handshake and hijacks the HTTP
// connection into a WebSocket. The caller must have authenticated the request
// before upgrading: the API accepts the bearer access token in the
// Authorization header, or (browser clients, which cannot set headers on a
// WebSocket) a bearer subprotocol the caller already verified. Access tokens
// in query strings are never accepted.
//
// When protocol is non-empty it is echoed as the selected
// Sec-WebSocket-Protocol. The caller owns subprotocol negotiation; Upgrade
// only constrains it to a valid header token so response splitting is
// impossible.
func Upgrade(writer http.ResponseWriter, request *http.Request, protocol string) (*Conn, error) {
	if request.Method != http.MethodGet {
		return nil, fmt.Errorf("websocket upgrade requires GET")
	}
	if !headerContainsToken(request.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("websocket upgrade requires Upgrade: websocket")
	}
	if !headerContainsToken(request.Header.Get("Connection"), "upgrade") {
		return nil, fmt.Errorf("websocket upgrade requires Connection: upgrade")
	}
	if strings.TrimSpace(request.Header.Get("Sec-WebSocket-Version")) != "13" {
		return nil, fmt.Errorf("websocket upgrade requires Sec-WebSocket-Version: 13")
	}
	key := strings.TrimSpace(request.Header.Get("Sec-WebSocket-Key"))
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		return nil, fmt.Errorf("websocket upgrade requires a valid Sec-WebSocket-Key")
	}
	if strings.TrimSpace(protocol) != protocol || strings.ContainsAny(protocol, "\r\n") {
		return nil, fmt.Errorf("websocket subprotocol must be a single header token")
	}
	hijacker, ok := writer.(http.Hijacker)
	if !ok {
		return nil, fmt.Errorf("websocket upgrade requires a hijackable connection")
	}
	sum := sha1.Sum([]byte(key + wsGUID)) // #nosec G401 -- RFC 6455 Section 1.3 mandates SHA-1 for the handshake accept key; not a security use.
	accept := base64.StdEncoding.EncodeToString(sum[:])

	conn, reader, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("websocket hijack: %w", err)
	}
	var response strings.Builder
	response.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ")
	response.WriteString(accept)
	response.WriteString("\r\n")
	if protocol != "" {
		response.WriteString("Sec-WebSocket-Protocol: ")
		response.WriteString(protocol)
		response.WriteString("\r\n")
	}
	response.WriteString("\r\n")
	if _, err := io.WriteString(conn, response.String()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake write: %w", err)
	}
	return &Conn{conn: conn, reader: reader.Reader}, nil
}

func headerContainsToken(header, token string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// Conn is a server-side WebSocket connection. Reads expect masked client
// frames per RFC 6455; writes are unmasked server frames. It is safe for one
// concurrent reader and one concurrent writer, matching the runapi stream
// pattern of a writer goroutine plus a reader that only watches for close.
type Conn struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

// Close sends a normal-closure frame best-effort and closes the connection.
func (c *Conn) Close() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	payload := []byte{0x03, 0xE8} // 1000 normal closure.
	frame := []byte{0x80 | wsOpClose, byte(len(payload))}
	frame = append(frame, payload...)
	_, _ = c.conn.Write(frame)
	return c.conn.Close()
}

// WriteText sends one unmasked text message carrying a JSON event with the
// same contract as the SSE fallback (type/code/message/run/thread/turn).
func (c *Conn) WriteText(payload []byte) error {
	if len(payload) > maxWSMessageBytes {
		return fmt.Errorf("websocket message exceeds %d bytes", maxWSMessageBytes)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	header := []byte{0x80 | wsOpText}
	switch {
	case len(payload) <= 125:
		header = append(header, byte(len(payload)))
	case len(payload) <= 65535:
		header = append(header, 126, byte(len(payload)>>8), byte(len(payload)))
	default:
		header = append(header, 127, 0, 0, 0, 0,
			byte(len(payload)>>24), byte(len(payload)>>16), byte(len(payload)>>8), byte(len(payload)))
	}
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	_, err := c.conn.Write(payload)
	return err
}

// ReadMessage returns the next complete data message from the client,
// reassembling continuation frames up to the message bound. Ping frames are
// answered with pong automatically; a close frame is echoed and reported as
// io.EOF so stream loops terminate the same way for both transports.
func (c *Conn) ReadMessage() (opcode int, payload []byte, err error) {
	var message []byte
	var messageOp = -1
	for {
		op, fin, chunk, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case wsOpPing:
			if err := c.writeControl(wsOpPong, chunk); err != nil {
				return 0, nil, err
			}
			continue
		case wsOpPong:
			continue
		case wsOpClose:
			_ = c.writeControl(wsOpClose, chunk)
			return 0, nil, io.EOF
		case wsOpText, wsOpBinary:
			if messageOp != -1 {
				return 0, nil, fmt.Errorf("websocket protocol error: new message before fragmented message completed")
			}
			messageOp = op
			message = chunk
		case wsOpContinuation:
			if messageOp == -1 {
				return 0, nil, fmt.Errorf("websocket protocol error: stray continuation frame")
			}
			message = append(message, chunk...)
		default:
			return 0, nil, fmt.Errorf("websocket protocol error: unsupported opcode %d", op)
		}
		if len(message) > maxWSMessageBytes {
			return 0, nil, fmt.Errorf("websocket message exceeds %d bytes", maxWSMessageBytes)
		}
		if fin {
			return messageOp, message, nil
		}
	}
}

// readFrame parses one client frame. Client frames must be masked; control
// frames must be unfragmented with at most 125 bytes of payload.
func (c *Conn) readFrame() (opcode int, fin bool, payload []byte, err error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(c.reader, header); err != nil {
		return 0, false, nil, err
	}
	fin = header[0]&0x80 != 0
	opcode = int(header[0] & 0x0F)
	masked := header[1]&0x80 != 0
	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		extended := make([]byte, 2)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return 0, false, nil, err
		}
		length = int64(binary.BigEndian.Uint16(extended))
	case 127:
		extended := make([]byte, 8)
		if _, err := io.ReadFull(c.reader, extended); err != nil {
			return 0, false, nil, err
		}
		length = int64(binary.BigEndian.Uint64(extended))
		if length < 0 {
			return 0, false, nil, fmt.Errorf("websocket protocol error: invalid length")
		}
	}
	if length > maxWSMessageBytes {
		return 0, false, nil, fmt.Errorf("websocket frame exceeds %d bytes", maxWSMessageBytes)
	}
	isControl := opcode >= wsOpClose
	if isControl && (!fin || length > 125) {
		return 0, false, nil, fmt.Errorf("websocket protocol error: fragmented or oversized control frame")
	}
	if !masked {
		return 0, false, nil, fmt.Errorf("websocket protocol error: client frames must be masked")
	}
	mask := make([]byte, 4)
	if _, err := io.ReadFull(c.reader, mask); err != nil {
		return 0, false, nil, err
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.reader, payload); err != nil {
		return 0, false, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return opcode, fin, payload, nil
}

func (c *Conn) writeControl(opcode int, payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("websocket control payload exceeds 125 bytes")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout)); err != nil {
		return err
	}
	defer func() { _ = c.conn.SetWriteDeadline(time.Time{}) }()
	frame := []byte{0x80 | byte(opcode), byte(len(payload))}
	frame = append(frame, payload...)
	_, err := c.conn.Write(frame)
	return err
}
