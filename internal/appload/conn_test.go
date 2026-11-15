//go:build linux

package appload

import (
	"encoding/binary"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeAppLoad is AppLoad's end of the socket, written with the same system calls AppLoad uses (src/management.cpp) so the tests exercise the packet boundaries it relies on.
type fakeAppLoad struct {
	t        *testing.T
	listenFD int
	fd       int
	Path     string
}

func newFakeAppLoad(t *testing.T) *fakeAppLoad {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.sock")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: path}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Listen(fd, 1); err != nil {
		t.Fatal(err)
	}
	f := &fakeAppLoad{t: t, listenFD: fd, fd: -1, Path: path}
	t.Cleanup(func() {
		syscall.Close(f.listenFD)
		if f.fd >= 0 {
			syscall.Close(f.fd)
		}
	})
	return f
}

func (f *fakeAppLoad) accept() {
	f.t.Helper()
	fd, _, err := syscall.Accept(f.listenFD)
	if err != nil {
		f.t.Fatal(err)
	}
	tv := syscall.Timeval{Sec: 5}
	syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
	f.fd = fd
}

// send mirrors sendMessageTo: a header packet, then a contents packet even when the contents are empty.
func (f *fakeAppLoad) send(msgType int32, contents string) {
	f.t.Helper()
	var header [8]byte
	binary.NativeEndian.PutUint32(header[0:4], uint32(msgType))
	binary.NativeEndian.PutUint32(header[4:8], uint32(len(contents)))
	if err := syscall.Sendto(f.fd, header[:], 0, nil); err != nil {
		f.t.Fatal(err)
	}
	if err := syscall.Sendto(f.fd, []byte(contents), 0, nil); err != nil {
		f.t.Fatal(err)
	}
}

// receive mirrors _listeningThread: read a header, and read contents only when the header announces some.
func (f *fakeAppLoad) receive() (int32, string) {
	f.t.Helper()
	var header [8]byte
	n, err := syscall.Read(f.fd, header[:])
	if err != nil || n != 8 {
		f.t.Fatalf("header read: n=%d err=%v", n, err)
	}
	msgType := int32(binary.NativeEndian.Uint32(header[0:4]))
	length := int(binary.NativeEndian.Uint32(header[4:8]))
	if length == 0 {
		return msgType, ""
	}
	buf := make([]byte, length)
	n, err = syscall.Read(f.fd, buf)
	if err != nil || n != length {
		f.t.Fatalf("contents read: n=%d want %d err=%v", n, length, err)
	}
	return msgType, string(buf)
}

func (f *fakeAppLoad) close() {
	syscall.Close(f.fd)
	f.fd = -1
}

func dial(t *testing.T, f *fakeAppLoad) *Conn {
	t.Helper()
	conn, err := Dial(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	f.accept()
	return conn
}

func TestReceive(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	f.send(TypeFrontendAttached, "1")
	f.send(7, `{"action":"list","note":"héllo"}`)

	for _, want := range []Message{
		{Type: TypeFrontendAttached, Contents: "1"},
		{Type: 7, Contents: `{"action":"list","note":"héllo"}`},
	} {
		got, err := conn.Receive()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

func TestReceiveEmptyContentsDoesNotEndTheStream(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	f.send(3, "")
	f.send(4, "after")

	got, err := conn.Receive()
	if err != nil || got != (Message{Type: 3}) {
		t.Fatalf("empty message: got %+v, err %v", got, err)
	}
	got, err = conn.Receive()
	if err != nil || got != (Message{Type: 4, Contents: "after"}) {
		t.Fatalf("message after an empty one: got %+v, err %v", got, err)
	}
}

func TestReceiveReturnsEOFWhenAppLoadCloses(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	f.send(TypeTerminate, "close")
	f.close()

	got, err := conn.Receive()
	if err != nil || got != (Message{Type: TypeTerminate, Contents: "close"}) {
		t.Fatalf("got %+v, err %v", got, err)
	}
	if _, err := conn.Receive(); !errors.Is(err, io.EOF) {
		t.Errorf("want io.EOF after close, got %v", err)
	}
}

func TestReceiveRejectsOversizedAnnouncement(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	var header [8]byte
	binary.NativeEndian.PutUint32(header[0:4], 1)
	binary.NativeEndian.PutUint32(header[4:8], MaxMessageLength+1)
	if err := syscall.Sendto(f.fd, header[:], 0, nil); err != nil {
		t.Fatal(err)
	}

	if _, err := conn.Receive(); err == nil {
		t.Error("want an error for a message over the protocol limit")
	}
}

func TestReceiveReportsCloseBetweenHeaderAndContents(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	var header [8]byte
	binary.NativeEndian.PutUint32(header[0:4], 1)
	binary.NativeEndian.PutUint32(header[4:8], 5)
	if err := syscall.Sendto(f.fd, header[:], 0, nil); err != nil {
		t.Fatal(err)
	}
	f.close()

	if _, err := conn.Receive(); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("want io.ErrUnexpectedEOF, got %v", err)
	}
}

func TestSend(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	big := strings.Repeat("x", 40000)
	if err := conn.Send(1, "hello"); err != nil {
		t.Fatal(err)
	}
	if err := conn.Send(2, ""); err != nil {
		t.Fatal(err)
	}
	if err := conn.Send(3, big); err != nil {
		t.Fatal(err)
	}

	for _, want := range []Message{{1, "hello"}, {2, ""}, {3, big}} {
		gotType, gotContents := f.receive()
		if gotType != want.Type || gotContents != want.Contents {
			t.Errorf("AppLoad read type %d with %d bytes, want type %d with %d bytes", gotType, len(gotContents), want.Type, len(want.Contents))
		}
	}
}

func TestSendRejectsOversizedMessage(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	if err := conn.Send(1, strings.Repeat("x", MaxMessageLength+1)); err == nil {
		t.Error("want an error for a message over the protocol limit")
	}
}

func TestSendFromManyGoroutinesKeepsPacketsPaired(t *testing.T) {
	f := newFakeAppLoad(t)
	conn := dial(t, f)

	const senders, perSender = 8, 50
	var wg sync.WaitGroup
	for s := range senders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			contents := strings.Repeat(string(rune('a'+s)), 100+s)
			for range perSender {
				if err := conn.Send(int32(s), contents); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}

	for range senders * perSender {
		msgType, contents := f.receive()
		want := strings.Repeat(string(rune('a'+msgType)), 100+int(msgType))
		if contents != want {
			t.Fatalf("type %d arrived with another message's contents", msgType)
		}
	}
	wg.Wait()
}

func TestDialFailsWithoutAppLoad(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		_, err := Dial(filepath.Join(t.TempDir(), "missing.sock"))
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("want an error when nothing is listening")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Dial hung")
	}
}
