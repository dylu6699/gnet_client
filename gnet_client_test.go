package gnet_client

import (
	"strconv"
	"testing"
	"time"

	"github.com/panjf2000/gnet"
)

type testClient struct {
	*EventClient
	network string
}

var gt *testing.T

func (s *testClient) React(frame []byte, c Conn) (out []byte, action gnet.Action) {

	if frame == nil || len(frame) <= 0 || c == nil {
		gt.Logf("eframe == nil, %v, %v, %v", frame, len(frame), c)
		panic("exit")
		return
	}
	gt.Logf("read data[%d]:%s\n", len(frame), string(frame))
	out = frame
	return
}
func (s *testClient) OnClosed(c Conn, err error) (action gnet.Action) {
	if err != nil {
		gt.Logf("error occurred on closed, %v\n", err)
	}
	if c.Context() != c {
		panic("invalid context")
	}
	return
}

func TestClient(t *testing.T) {
	gt = t
	//client("127.0.0.1", 9991)
	ts := &testClient{
		network: "tcp",
		//workerPool: goroutine.Default(),
	}
	el, err := NewEventloop(ts)
	if err != nil {
		t.Logf("NewEventloop err: %v\n", err)
	}
	el.StartEventLoops()
	num := 100
	cliM := make([]*Client, num)
	for i := 0; i < num; i++ {
		cli, err := NewClient(el, "tcp", "127.0.0.1:9991")
		if err != nil {
			t.Logf("NewClient err: %v\n", err)
			return
		}
		cliM[i] = cli
		time.Sleep(10 * time.Microsecond)
	}

	for i := 0; i < num; i++ {
		//b := rand.Intn(len(cliM)) //生成0-99之间的随机数
		data := "My name is ldy "
		data += strconv.Itoa(i)
		err := cliM[i].conn.AsyncWrite([]byte(data))
		if err != nil {
			t.Logf("error conn.AsyncWrite:%v\n", err)
		}
		//time.Sleep(10 * time.Microsecond)
	}
	for {
		time.Sleep(1 * time.Second)
	}

}

// server source
/* If you want to run server, please copy the following code to another file and rename it to server.go , and then execute the command go run server.go
如果要运行server请拷贝下面代码到另一个文件下，重命名为server.go，然后执行命令 go run server.go
*/
/*package main

import (
	"fmt"
	"net"
)
func Server() {
	fmt.Println("process start...")
	addr := fmt.Sprintf("0.0.0.0:%d", 9991)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("err! addr %s open faild err:[%v]\n", addr, err)
		return
	}

	for {

		conn, err := listener.Accept()
		if err != nil {
			fmt.Printf("listen err:[%v]\n", err)
		}

		go handConn(conn)
	}

}
func handConn(conn net.Conn) {
	defer func() {
		fmt.Printf("client [%s] close\n", conn.RemoteAddr().String())
		conn.Close()
	}()
	var buf [1024]byte
	for {
		n, err := conn.Read(buf[:])
		if err != nil {
			fmt.Printf("read from %s msg faild err:[%v]\n", conn.RemoteAddr().String(), err)
			break
		}
		fmt.Printf("rev data from %s msg:%s\n", conn.RemoteAddr().String(), string(buf[:n]))
		conn.Write(buf[:n])
	}
}
*/
