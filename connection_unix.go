// Copyright (c) 2019 Andy Pan
// Copyright (c) 2018 Joshua J Baker
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// +build linux freebsd dragonfly darwin

package gnet_client

import (
	"gnet_client/internal/netpoll"
	"net"
	"os"

	"github.com/panjf2000/gnet"
	"github.com/panjf2000/gnet/pool/bytebuffer"
	prb "github.com/panjf2000/gnet/pool/ringbuffer"
	"github.com/panjf2000/gnet/ringbuffer"
	"golang.org/x/sys/unix"
)

// Conn is a interface of gnet connection.
type Conn interface {
	// Context returns a user-defined context.
	Context() (ctx interface{})

	// SetContext sets a user-defined context.
	SetContext(ctx interface{})

	// LocalAddr is the connection's local socket address.
	LocalAddr() (addr net.Addr)

	// RemoteAddr is the connection's remote peer address.
	RemoteAddr() (addr net.Addr)

	// Read reads all data from inbound ring-buffer and event-loop-buffer without moving "read" pointer, which means
	// it does not evict the data from buffers actually and those data will present in buffers until the
	// ResetBuffer method is called.
	Read() (buf []byte)

	// ResetBuffer resets the buffers, which means all data in inbound ring-buffer and event-loop-buffer will be evicted.
	ResetBuffer()

	// ReadN reads bytes with the given length from inbound ring-buffer and event-loop-buffer without moving
	// "read" pointer, which means it will not evict the data from buffers until the ShiftN method is called,
	// it reads data from the inbound ring-buffer and event-loop-buffer and returns both bytes and the size of it.
	// If the length of the available data is less than the given "n", ReadN will return all available data, so you
	// should make use of the variable "size" returned by it to be aware of the exact length of the returned data.
	ReadN(n int) (size int, buf []byte)

	// ShiftN shifts "read" pointer in the internal buffers with the given length.
	ShiftN(n int) (size int)

	// BufferLength returns the length of available data in the internal buffers.
	BufferLength() (size int)

	// InboundBuffer returns the inbound ring-buffer.
	// InboundBuffer() *ringbuffer.RingBuffer

	// SendTo writes data for UDP sockets, it allows you to send data back to UDP socket in individual goroutines.
	SendTo(buf []byte) error

	// AsyncWrite writes data to client/connection asynchronously, usually you would call it in individual goroutines
	// instead of the event-loop goroutines.
	AsyncWrite(buf []byte) error

	// Wake triggers a React event for this connection.
	Wake() error

	// Close closes the current connection.
	Close() error
}

type conn struct {
	fd             int                    // file descriptor
	sa             unix.Sockaddr          // remote socket address
	ctx            interface{}            // user-defined context
	loop           *Eventloop             // connected event-loop
	codec          gnet.ICodec            // codec for TCP
	buffer         []byte                 // reuse memory of inbound data as a temporary buffer
	opened         bool                   // connection opened event fired
	localAddr      net.Addr               // local addr
	remoteAddr     net.Addr               // remote addr
	byteBuffer     *bytebuffer.ByteBuffer // bytes buffer for buffering current packet and data in ring-buffer
	inboundBuffer  *ringbuffer.RingBuffer // buffer for data from client
	outboundBuffer *ringbuffer.RingBuffer // buffer for data that is ready to write to client
}

func newTCPConn(fd int, el *Eventloop, sa unix.Sockaddr) *conn {
	c := &conn{
		fd:             fd,
		sa:             sa,
		loop:           el,
		codec:          new(gnet.BuiltInFrameCodec),
		inboundBuffer:  prb.Get(),
		outboundBuffer: prb.Get(),
	}
	c.localAddr = nil
	c.remoteAddr = netpoll.SockaddrToTCPOrUnixAddr(sa)
	return c
}

func (c *conn) releaseTCP() {
	c.opened = false
	c.sa = nil
	c.ctx = nil
	c.buffer = nil
	c.localAddr = nil
	c.remoteAddr = nil
	prb.Put(c.inboundBuffer)
	prb.Put(c.outboundBuffer)
	c.inboundBuffer = nil
	c.outboundBuffer = nil
	bytebuffer.Put(c.byteBuffer)
	c.byteBuffer = nil
}

func newUDPConn(fd int, el *Eventloop, sa unix.Sockaddr) *conn {
	return &conn{
		fd:         fd,
		sa:         sa,
		localAddr:  nil,
		remoteAddr: netpoll.SockaddrToUDPAddr(sa),
	}
}

func (c *conn) releaseUDP() {
	c.ctx = nil
	c.localAddr = nil
	c.remoteAddr = nil
}

func (c *conn) open(buf []byte) {
	n, err := unix.Write(c.fd, buf)
	if err != nil {
		_, _ = c.outboundBuffer.Write(buf)
		return
	}

	if n < len(buf) {
		_, _ = c.outboundBuffer.Write(buf[n:])
	}
}

func (c *conn) read() ([]byte, error) {
	return c.codec.Decode(c)
}

func (c *conn) write(buf []byte) (err error) {
	var outFrame []byte
	if outFrame, err = c.codec.Encode(c, buf); err != nil {
		return
	}
	if !c.outboundBuffer.IsEmpty() {
		_, _ = c.outboundBuffer.Write(outFrame)
		return
	}

	var n int
	if n, err = unix.Write(c.fd, outFrame); err != nil {
		if err == unix.EAGAIN {
			_, _ = c.outboundBuffer.Write(outFrame)
			err = c.loop.poller.ModReadWrite(c.fd)
			return
		}
		return c.loop.loopCloseConn(c, os.NewSyscallError("write", err))
	}
	if n < len(outFrame) {
		_, _ = c.outboundBuffer.Write(outFrame[n:])
		err = c.loop.poller.ModReadWrite(c.fd)
	}
	return
}

func (c *conn) sendTo(buf []byte) error {
	return unix.Sendto(c.fd, buf, 0, c.sa)
}

// ================================= Public APIs of gnet.Conn =================================

func (c *conn) Read() []byte {
	if c.inboundBuffer.IsEmpty() {
		return c.buffer
	}
	c.byteBuffer = c.inboundBuffer.WithByteBuffer(c.buffer)
	return c.byteBuffer.Bytes()
}

func (c *conn) ResetBuffer() {
	c.buffer = c.buffer[:0]
	c.inboundBuffer.Reset()
	bytebuffer.Put(c.byteBuffer)
	c.byteBuffer = nil
}

func (c *conn) ReadN(n int) (size int, buf []byte) {
	inBufferLen := c.inboundBuffer.Length()
	tempBufferLen := len(c.buffer)
	if totalLen := inBufferLen + tempBufferLen; totalLen < n || n <= 0 {
		n = totalLen
	}
	size = n
	if c.inboundBuffer.IsEmpty() {
		buf = c.buffer[:n]
		return
	}
	head, tail := c.inboundBuffer.LazyRead(n)
	c.byteBuffer = bytebuffer.Get()
	_, _ = c.byteBuffer.Write(head)
	_, _ = c.byteBuffer.Write(tail)
	if inBufferLen >= n {
		buf = c.byteBuffer.Bytes()
		return
	}

	restSize := n - inBufferLen
	_, _ = c.byteBuffer.Write(c.buffer[:restSize])
	buf = c.byteBuffer.Bytes()
	return
}

func (c *conn) ShiftN(n int) (size int) {
	inBufferLen := c.inboundBuffer.Length()
	tempBufferLen := len(c.buffer)
	if inBufferLen+tempBufferLen < n || n <= 0 {
		c.ResetBuffer()
		size = inBufferLen + tempBufferLen
		return
	}
	size = n
	if c.inboundBuffer.IsEmpty() {
		c.buffer = c.buffer[n:]
		return
	}

	bytebuffer.Put(c.byteBuffer)
	c.byteBuffer = nil

	if inBufferLen >= n {
		c.inboundBuffer.Shift(n)
		return
	}
	c.inboundBuffer.Reset()

	restSize := n - inBufferLen
	c.buffer = c.buffer[restSize:]
	return
}

func (c *conn) BufferLength() int {
	return c.inboundBuffer.Length() + len(c.buffer)
}

func (c *conn) AsyncWrite(buf []byte) error {
	return c.loop.poller.Trigger(func() (err error) {
		if c.opened {
			err = c.write(buf)
		}
		return
	})
}

func (c *conn) SendTo(buf []byte) error {
	return c.sendTo(buf)
}

func (c *conn) Wake() error {
	return c.loop.poller.Trigger(func() error {
		return c.loop.loopWake(c)
	})
}

func (c *conn) Close() error {
	return c.loop.poller.Trigger(func() error {
		return c.loop.loopCloseConn(c, nil)
	})
}

func (c *conn) Context() interface{}       { return c.ctx }
func (c *conn) SetContext(ctx interface{}) { c.ctx = ctx }
func (c *conn) LocalAddr() net.Addr        { return c.localAddr }
func (c *conn) RemoteAddr() net.Addr       { return c.remoteAddr }
