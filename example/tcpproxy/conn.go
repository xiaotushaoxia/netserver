package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync/atomic"
	"syscall"
)

type Proxy struct {
	logger logger

	raddr  *net.TCPAddr
	cidSeq atomic.Uint64
}

func NewProxy(raddrs string, bl logger) (*Proxy, error) {
	raddr, err := net.ResolveTCPAddr("tcp", raddrs)
	if err != nil {
		return nil, err
	}

	if bl == nil {
		bl = noopLogger{}
	}
	return &Proxy{
		logger: &headLogger{
			head: "[Proxy to " + raddrs + "]",
			l:    bl,
		},
		raddr: raddr,
	}, nil
}

func (m *Proxy) newConn(ctx context.Context, conn net.Conn) {
	cid := m.cidSeq.Add(1)
	cl := &headLogger{
		head: fmt.Sprintf("[Seq %d, from %s]", cid, conn.RemoteAddr()),
		l:    m.logger,
	}
	cl.Printf("new conn")
	client, ok := conn.(*net.TCPConn)
	if !ok {
		cl.Printf("not *net.TCPConn, is %T, close client err: %v", conn, conn.Close())
		return
	}
	backend, er := net.DialTCP("tcp", nil, m.raddr)
	if er != nil {
		cl.Printf("dial backend error:%s, close client err: %s", er, conn.Close())
		return
	}

	m.handle(ctx, cl, client, backend)

}

func (m *Proxy) handle(ctx context.Context, cl logger, client, backend *net.TCPConn) {
	ctxExit, cancel := context.WithCancel(context.Background())
	defer cancel()
	brokerExit := make(chan string, 2)
	defer func() {
		close(brokerExit)
		cancel()
		//  即使这个函数的末尾有这两个close， close还是要放在defer里面踏实一点。
		backend.Close()
		client.Close()
	}()

	broker := func(dst *net.TCPConn, src *net.TCPConn, msg string) {
		mm := "服务器->客户端"
		if msg == "broker: client to backend" {
			mm = "客户端->服务器"
		}
		wrote, err := io.Copy(io.MultiWriter(dst, &writeLogger{head: mm}), src)
		// todo 这个行为不一定是正确的 我参考了 github.com/docker/go-connections的TCPProxy
		if err != nil && isPeerCloseRead(err) { //
			cl.Printf(" forward SHUT_WR to the other end of the pipe !!!!")
			src.CloseRead()
		}
		dst.CloseWrite()
		cl.Printf("Summary: %s %d bytes, exit err:%v", msg, wrote, err)
		brokerExit <- msg
	}

	go broker(client, backend, "broker: backend to client")
	go broker(backend, client, "broker: client to backend")

	go func() {
		i := 0
		for {
			select {
			case msg := <-brokerExit:
				cl.Printf("broker exit: " + msg)
				i++
			}
			if i == 2 {
				cancel()
				break
			}
		}
	}()

	select {
	case <-ctxExit.Done():
		backend.Close()
		client.Close()
	case <-ctx.Done():
		backend.Close()
		client.Close()
		<-ctxExit.Done()
	}
	cl.Printf("exit")
}

type writeLogger struct {
	head string
}

func (w *writeLogger) Write(bs []byte) (int, error) {
	ob := len(bs)
	a := []byte("\n" + w.head + fmt.Sprintf(": %d:", len(bs)))
	bs = []byte(fmt.Sprintf("% X", bs))
	os.Stdout.Write(append(a, bs...))
	return ob, nil
}

func isPeerCloseRead(err error) bool {
	// github.com/docker/go-connections的TCPProxy这样做 但是好像是错误
	// https://github.com/docker/go-connections/issues/110
	// 而且我发现对端SHUT_WR后，尽管在linux上也不一定读到syscall.EPIPE 也可能是syscall.ECONNRESET
	// 而且win上是syscall.WSAECONNRESET和syscall.WSAECONNABORTED，根本不会返回syscall.EPIPE
	//if er, ok := err.(*net.OpError); ok && er.Err == syscall.EPIPE {
	//	return true
	//} else {
	//	return false
	//}

	var errno syscall.Errno
	if errors.As(err, &errno) {
		return false
	}
	// windows
	// syscall.WSAECONNRESET 10054 "wsasend: An existing connection was forcibly closed by the remote host."
	// syscall.WSAECONNABORTED 10053 wsasend: An established connection was aborted by the software in your host machine.

	// linux
	// syscall.EPIPE 32 broken pipe
	// syscall.ECONNRESET 104 connection reset by peer
	if int(errno) == 10054 || int(errno) == 10053 || int(errno) == 32 || int(errno) == 104 {
		// 不单独写_linux和_window文件了，直接两个放一起判断
		return true
	}
	return false
}
