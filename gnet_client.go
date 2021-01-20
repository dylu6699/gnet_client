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

package gnet_client

import "github.com/panjf2000/gnet"

type (
	EventHandler interface {
		OnClosed(c Conn, err error) (action gnet.Action)
		React(frame []byte, c Conn) (out []byte, action gnet.Action)
	}
	EventClient struct {
	}
)

// OnClosed fires when a connection has been closed.
// The parameter:err is the last known connection error.
func (es *EventClient) OnClosed(c Conn, err error) (action gnet.Action) {
	return
}

// React fires when a connection sends the server data.
// Call c.Read() or c.ReadN(n) within the parameter:c to read incoming data from client.
// Parameter:out is the return value which is going to be sent back to the client.
func (es *EventClient) React(frame []byte, c Conn) (out []byte, action gnet.Action) {
	return
}

type Client struct {
	conn     Conn
	mainLoop *Eventloop // main event-loop for accepting connections
}

// NewClient ...
func NewClient(el *Eventloop, network string, address string) (cli *Client, err error) {
	c, err := SocketConn(el, network, address)
	if err != nil {
		return nil, err
	}
	err = el.LoopAddConn(c)
	if err != nil {
		c.Close()
		return nil, err
	}

	cli = &Client{conn: c, mainLoop: el}
	return cli, nil
}
