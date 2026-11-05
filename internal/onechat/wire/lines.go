// Package wire frames protocol v1 messages for the local control socket.
package wire

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

const MaxMessageSize = 1 << 20

// ReadLine requires a newline, so a truncated request at EOF is never executed.
// The reader's buffer may be small; accumulated fragments remain bounded.
func ReadLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > MaxMessageSize+1 {
			return nil, fmt.Errorf("onechat message exceeds %d bytes", MaxMessageSize)
		}
		line = append(line, part...)
		if err == nil {
			return line[:len(line)-1], nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if len(line) > MaxMessageSize {
			return nil, fmt.Errorf("onechat message exceeds %d bytes", MaxMessageSize)
		}
	}
}

func WriteLine(writer io.Writer, payload []byte) error {
	if len(payload) > MaxMessageSize {
		return fmt.Errorf("onechat message exceeds %d bytes", MaxMessageSize)
	}
	if bytes.IndexByte(payload, '\n') >= 0 {
		return fmt.Errorf("onechat message must fit on one JSON line")
	}
	if err := writeAll(writer, payload); err != nil {
		return err
	}
	return writeAll(writer, []byte{'\n'})
}

func writeAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
