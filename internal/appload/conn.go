//go:build linux

// Package appload speaks the protocol between AppLoad and an app's backend process.
//
// AppLoad starts the backend with a socket path as its first argument. Messages
// on that socket carry a numeric type the app chooses and a UTF-8 string;
// negative types are AppLoad's own.
package appload

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
)

const (
	// TypeTerminate arrives when AppLoad is shutting the backend down.
	TypeTerminate int32 = -1
	// TypeFrontendAttached and TypeFrontendDetached arrive when a frontend connects to or leaves the backend; Contents is how many remain, in decimal.
	TypeFrontendAttached int32 = -2
	TypeFrontendDetached int32 = -3

	// MaxMessageLength is AppLoad's own limit. A message also has to fit in one packet of the socket's send buffer, which is far smaller, so keep messages to tens of kilobytes.
	MaxMessageLength = 10 << 20

	headerSize = 8
)

type Message struct {
	Type     int32
	Contents string
}

type Conn struct {
	conn *net.UnixConn

	sendMu sync.Mutex
	recvMu sync.Mutex
}

func Dial(socketPath string) (*Conn, error) {
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: socketPath, Net: "unixpacket"})
	if err != nil {
		return nil, fmt.Errorf("connect to AppLoad: %w", err)
	}
	return &Conn{conn: conn}, nil
}

func (c *Conn) Close() error {
	return c.conn.Close()
}

// Send is safe to call from several goroutines.
func (c *Conn) Send(msgType int32, contents string) error {
	if len(contents) > MaxMessageLength {
		return fmt.Errorf("message of %d bytes is over AppLoad's %d byte limit", len(contents), MaxMessageLength)
	}
	var header [headerSize]byte
	binary.NativeEndian.PutUint32(header[0:4], uint32(msgType))
	binary.NativeEndian.PutUint32(header[4:8], uint32(len(contents)))

	// AppLoad reads the header and the contents as two packets, so nothing may come between them.
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if _, err := c.conn.Write(header[:]); err != nil {
		return fmt.Errorf("send header: %w", err)
	}
	// AppLoad does not read a contents packet for an empty message.
	if len(contents) == 0 {
		return nil
	}
	if _, err := c.conn.Write([]byte(contents)); err != nil {
		return fmt.Errorf("send contents: %w", err)
	}
	return nil
}

// Receive blocks until a message arrives and returns io.EOF once AppLoad has closed the connection.
func (c *Conn) Receive() (Message, error) {
	c.recvMu.Lock()
	defer c.recvMu.Unlock()

	var header [headerSize]byte
	n, err := c.conn.Read(header[:])
	if err != nil {
		return Message{}, err
	}
	if n != headerSize {
		return Message{}, fmt.Errorf("short header from AppLoad: %d bytes", n)
	}
	msgType := int32(binary.NativeEndian.Uint32(header[0:4]))
	length := int32(binary.NativeEndian.Uint32(header[4:8]))
	if length < 0 || length > MaxMessageLength {
		return Message{}, fmt.Errorf("AppLoad announced a message of %d bytes", length)
	}

	// AppLoad sends a contents packet even when it is empty, and an empty packet reads the same as a closed connection, so that one case is not an error here; a real close shows up on the next Receive.
	contents := make([]byte, max(int(length), 1))
	n, err = c.conn.Read(contents)
	if err != nil && !(length == 0 && errors.Is(err, io.EOF)) {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Message{}, fmt.Errorf("read contents: %w", err)
	}
	if n != int(length) {
		return Message{}, fmt.Errorf("AppLoad announced %d bytes and sent %d", length, n)
	}
	return Message{Type: msgType, Contents: string(contents[:n])}, nil
}
