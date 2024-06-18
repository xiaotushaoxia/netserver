package netserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/xiaotushaoxia/errx"
)

func (s *NetServer) RunTLS(ctx context.Context, addr, certFile, keyFile string) error {
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return failedTo(err, "tls.LoadX509KeyPair")
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{certificate},
	}

	return s.RunTLSConfig(ctx, addr, cfg)
}

func (s *NetServer) RunTLSConfig(ctx context.Context, addr string, cfg *tls.Config) error {
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		return failedTo(err, "listen tcp")
	}
	return s.RunListener(ctx, tls.NewListener(listen, cfg))
}

func (s *NetServer) RunTCP(ctx context.Context, addr string) error {
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		return failedTo(err, "listen tcp")
	}
	return s.RunListener(ctx, listen)
}

func (s *NetServer) RunUnix(ctx context.Context, file string) error {
	listener, err := net.Listen("unix", file)
	if err != nil {
		return failedTo(err, "listen unix")
	}
	return s.RunListener(ctx, listener)
}

func (s *NetServer) RunFD(ctx context.Context, fd int) error {
	f := os.NewFile(uintptr(fd), fmt.Sprintf("fd@%d", fd))
	listener, err := net.FileListener(f)
	if err != nil {
		return failedTo(err, "listen fd")
	}
	return s.RunListener(ctx, listener)
}

func (s *NetServer) RunListener(ctx context.Context, l net.Listener) error {
	s.logFunc("server run on addr: %s", l.Addr())

	acceptErr := make(chan error, 1)
	connCtx, cancel := context.WithCancel(ctx)

	go func() {
		if er := s.acceptLoop(connCtx, l); er != nil {
			acceptErr <- er
		}
	}()

	defer func() {
		s.closeListener(l)
		cancel()
		s.cleanUp()
	}()

	select {
	case <-ctx.Done():
		er := getCtxCauseErr(ctx)
		s.logFunc("close listener because ctx done: %s", er)
		return errx.WithMessage(er, "ctx done")
	case er := <-acceptErr:
		s.logFunc("close listener because accept error: %s", er)
		return errx.WithMessage(er, "accept error")
	}
}

func (s *NetServer) acceptLoop(connCtx context.Context, l net.Listener) error {
	delayer := retryDelay{maxDelay: time.Second, initDelay: time.Millisecond * 5}
	for {
		conn, er := l.Accept()
		if er == nil { // good path
			delayer.reset()
			go s.newConn(connCtx, s.count.Add(1), conn)
			continue
		}
		select {
		case <-connCtx.Done():
			return nil
		default:
		}
		s.logFunc("accept error: %v", er)
		if !isTemporary(er) {
			s.logFunc("not temporary error exit")
			return er
		}

		s.logFunc("accept temporary error: retrying in %v", delayer.next())
		if interrupted := delayer.delay(connCtx); interrupted {
			s.logFunc("ctx done when delay")
			return nil
		}
	}
}

func (s *NetServer) newConn(root context.Context, id uint64, conn net.Conn) {
	c := s.addConn(root, id, conn)
	defer s.deleteConn(id, conn)
	s.HandleFunc(c.Ctx, conn)
}

func (s *NetServer) addConn(root context.Context, id uint64, conn net.Conn) *ClientConn {
	head := getName(id, conn)
	ctx, cancelFunc := context.WithCancel(root)
	s.logFunc("%s: connected", head)
	var once sync.Once
	cancelFunc2 := func() {
		cancelFunc()
		once.Do(func() {
			s.cancelOneClient(id)
		})
	}
	c := &ClientConn{Conn: conn, Id: id, Ctx: ctx, Cancel: cancelFunc2, StartTime: time.Now()}
	s.conns.Store(id, c)
	s.activeConn.Add(1)
	return c
}

func (s *NetServer) deleteConn(id uint64, conn net.Conn) {
	head := getName(id, conn)
	s.logFunc("%s: handle func exited", head)
	s.activeConn.Add(^uint64(0)) // -1
	s.conns.Delete(id)
	// HandleFunc should call conn.Close. call twice to make sure conn is released
	// fixme call Close twice looks make no side effects
	s.logFunc("close %s at last. err: %v", head, swallowErrClosed(conn.Close()))
	s.logFunc("%s: disconnected", head)
}

func (s *NetServer) cancelOneClient(id uint64) {
	go func() {
		fmt.Println("cancelOneClient", id)
		s.waitOneClientExit(id, s.handleFuncCancelTimeout)
		value, ok := s.conns.Load(id)
		if !ok {
			return
		}
		if !s.forceCloseClientIfTimeout {
			s.logFunc("warn: ForceCloseClientIfTimeout disabled. conn %d still alive", id)
			return
		}
		s.logFunc("ForceCloseClientIfTimeout enabled. try Close Conn and wait")
		s.logFunc("close %s, err: %v", getName(value.Id, value.Conn), swallowErrClosed(value.Conn.Close()))
		s.waitClientExit(s.closeClientTimeout)
	}()

}
