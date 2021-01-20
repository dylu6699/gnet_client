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
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"gnet_client/internal/netpoll"

	"github.com/panjf2000/gnet"
	gerrors "github.com/panjf2000/gnet/errors"
	"golang.org/x/sys/unix"
)

type Eventloop struct {
	internalEventloop

	// Prevents Eventloop from false sharing by padding extra memory with the difference
	// between the cache line size "s" and (Eventloop mod s) for the most common CPU architectures.
	_ [64 - unsafe.Sizeof(internalEventloop{})%64]byte
}

type internalEventloop struct {
	sync.RWMutex
	poller       *netpoll.Poller // epoll or kqueue
	packet       []byte          // read packet buffer
	connCount    int32           // number of active connections in event-loop
	connections  map[int]*conn   // loop connections fd -> conn
	eventHandler EventHandler    // user eventHandler
}

func NewEventloop(ec EventHandler) (el *Eventloop, err error) {

	if p, err := netpoll.OpenPoller(); err == nil {
		el = new(Eventloop)
		el.poller = p
		el.packet = make([]byte, 0x10000)
		el.connections = make(map[int]*conn)
	} else {
		return nil, err
	}
	el.eventHandler = ec
	return el, nil
}
func (el *Eventloop) StartEventLoops() {
	go el.loopRun()
}
func (el *Eventloop) CloseEventLoops() {
	_ = el.poller.Close()
}

func (el *Eventloop) loopRun() {
	el.poller.Polling(el.handleEvent)
}

// func (el *Eventloop) loopOpen(c *conn) error {
// 	c.opened = true

// 	if !c.outboundBuffer.IsEmpty() {

// 		_ = el.poller.AddWrite(c.fd)
// 	}

// 	return nil
// }

func (el *Eventloop) loopRead(c *conn) error {
	n, err := unix.Read(c.fd, el.packet)
	if n == 0 || err != nil {
		if err == unix.EAGAIN {
			return nil
		}
		return el.loopCloseConn(c, os.NewSyscallError("read", err))
	}
	c.buffer = el.packet[:n]

	for inFrame, _ := c.read(); inFrame != nil; inFrame, _ = c.read() {
		out, action := el.eventHandler.React(inFrame, c)
		if out != nil {
			if err = c.write(out); err != nil {
				return err
			}
		}
		switch action {
		case gnet.None:
		case gnet.Close:
			return el.loopCloseConn(c, nil)
		case gnet.Shutdown:
			return gerrors.ErrServerShutdown
		}

		// Check the status of connection every loop since it might be closed during writing data back to client due to
		// some kind of system error.
		if !c.opened {
			return nil
		}
	}
	_, _ = c.inboundBuffer.Write(c.buffer)

	return nil
}

func (el *Eventloop) loopWrite(c *conn) error {
	head, tail := c.outboundBuffer.LazyReadAll()
	n, err := unix.Write(c.fd, head)
	if err != nil {
		if err == unix.EAGAIN {
			return nil
		}
		return el.loopCloseConn(c, os.NewSyscallError("write", err))
	}
	c.outboundBuffer.Shift(n)

	if n == len(head) && tail != nil {
		n, err = unix.Write(c.fd, tail)
		if err != nil {
			if err == unix.EAGAIN {
				return nil
			}
			return el.loopCloseConn(c, os.NewSyscallError("write", err))
		}
		c.outboundBuffer.Shift(n)
	}

	if c.outboundBuffer.IsEmpty() {
		_ = el.poller.ModRead(c.fd)
	}

	return nil
}

func (el *Eventloop) loopCloseConn(c *conn, err error) (rerr error) {
	if !c.opened {
		return fmt.Errorf("the fd=%d in event-loop is already closed, skipping it", c.fd)
	}

	// Send residual data in buffer back to client before actually closing the connection.
	if !c.outboundBuffer.IsEmpty() {
		head, tail := c.outboundBuffer.LazyReadAll()
		if n, err := unix.Write(c.fd, head); err == nil {
			if n == len(head) && tail != nil {
				_, _ = unix.Write(c.fd, tail)
			}
		}
	}

	if err0, err1 := el.poller.Delete(c.fd), unix.Close(c.fd); err0 == nil && err1 == nil {
		el.Lock()
		delete(el.connections, c.fd)
		el.Unlock()
		if el.eventHandler.OnClosed(c, err) == gnet.Shutdown {
			return gerrors.ErrServerShutdown
		}
		c.releaseTCP()
	} else {
		if err0 != nil {
			rerr = fmt.Errorf("failed to delete fd=%d from poller in event-loop: %v", c.fd, err0)
		}
		if err1 != nil {
			err1 = fmt.Errorf("failed to close fd=%d in event-loop: %v", c.fd, os.NewSyscallError("close", err1))
			if rerr != nil {
				rerr = errors.New(rerr.Error() + " & " + err1.Error())
			} else {
				rerr = err1
			}
		}
	}

	return
}

func (el *Eventloop) loopWake(c *conn) error {
	//if co, ok := el.connections[c.fd]; !ok || co != c {
	//	return nil // ignore stale wakes.
	//}
	out, action := el.eventHandler.React(nil, c)
	if out != nil {
		if err := c.write(out); err != nil {
			return err
		}
	}

	return el.handleAction(c, action)
}

func (el *Eventloop) handleAction(c *conn, action gnet.Action) error {
	switch action {
	case gnet.None:
		return nil
	case gnet.Close:
		return el.loopCloseConn(c, nil)
	case gnet.Shutdown:
		return gerrors.ErrServerShutdown
	default:
		return nil
	}
}

func (el *Eventloop) loopReadUDP(fd int) error {
	n, sa, err := unix.Recvfrom(fd, el.packet, 0)
	if err != nil {
		if err == unix.EAGAIN || err == unix.EWOULDBLOCK {
			return nil
		}
		return fmt.Errorf("failed to read UDP packet from fd=%d in event-loop %v",
			fd, os.NewSyscallError("recvfrom", err))
	}

	c := newUDPConn(fd, el, sa)
	out, action := el.eventHandler.React(el.packet[:n], c)
	if out != nil {
		_ = c.sendTo(out)
	}
	if action == gnet.Shutdown {
		return gerrors.ErrServerShutdown
	}
	c.releaseUDP()

	return nil
}

func (el *Eventloop) LoopAddConn(c *conn) (err error) {
	c.opened = true
	c.SetContext(c)
	if err = el.poller.AddReadWrite(c.fd); err == nil {
		el.Lock()
		el.connections[c.fd] = c
		el.Unlock()
		return err
		//return el.loopOpen(c)
	} else {
		fmt.Printf("el.poller.AddWrite err: %v,fd:%d\n", err, c.fd)
	}
	return err
}
