package netserver

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/xiaotushaoxia/ctxutils"
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
	defer cancel()

	go func() {
		if er := s.acceptLoop(connCtx, l); er != nil {
			acceptErr <- er
		}
	}()

	defer func() {
		s.closeListener(l)
		s.stop(cancel)
		s.waitClientExit()
	}()

	select {
	case <-ctx.Done():
		s.logFunc("close listener because ctx done")
		//// context.Cause(ctx) should eq to ctx.Err() or not nil. will fix in go1.22
		//// https://github.com/golang/go/issues/62582
		//return errx.WithMessage(ctx.Err(), "ctx done")
		if er := context.Cause(ctx); er != nil {
			// context.Cause(ctx) should eq to ctx.Err() or not nil. will fix in go1.22
			// https://github.com/golang/go/issues/62582
			return errx.WithMessage(er, "ctx done")
		}
		return errx.WithMessage(ctx.Err(), "ctx done")
	case er := <-acceptErr:
		s.logFunc("close listener because accept error %s", er)
		return errx.WithMessage(er, "accept error")
	}
}

func (s *NetServer) acceptLoop(connCtx context.Context, l net.Listener) error {
	//listener := newIgnoreTemporaryErrorListener(l)
	//go listener.back(connCtx) // exit when accept error or connCtx done
	//for {
	//	select {
	//	case err := <-listener.errCh:
	//		// 如果err==nil, 表示是connCtx Done了
	//		return err
	//	case conn := <-listener.connCh:
	//		go s.newConn(connCtx, s.count.Add(1), conn)
	//	}
	//}

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
		if !IsTemporary(er) {
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

func (s *NetServer) newConn(connCtx context.Context, id uint64, conn net.Conn) {
	head := getHead(id, conn)

	s.logFunc("%s connected", head)
	s.conns.Store(id, conn)
	s.activeConn.Add(1)

	wait := s.onShuttingDown(connCtx, id, conn)
	s.HandleFunc(connCtx, conn)
	wait()
	s.logFunc("%s onShuttingDown finish", head)

	s.logFunc("%s handle func exited", head)
	s.activeConn.Add(^uint64(0)) // -1
	s.conns.Delete(id)

	s.logFunc("%s close. err: %v", head, SwallowErrClosed(conn.Close()))
}

func (s *NetServer) onShuttingDown(connCtx context.Context, id uint64, conn net.Conn) (block func()) {
	ctx, cancel := context.WithCancel(context.Background())
	s.connShuttingDownCalled.Store(id, ctx)
	if !s.closeWriteWhenShuttingDown && s.ShuttingDownHandleFunc == nil {
		return func() {
			noopFunc()
			cancel()
		}
	}
	head := getHead(id, conn)
	stopf, exit := ctxutils.AfterFunc(connCtx, func() {
		if s.ShuttingDownHandleFunc != nil {
			s.logFunc("%s exec ShuttingDownHandleFunc", head)
			s.ShuttingDownHandleFunc(connCtx, conn)
		}
		if s.closeWriteWhenShuttingDown {
			s.logFunc("%s exec CloseWrite", head)
			if cw, ok := conn.(closeWriter); ok {
				err := cw.CloseWrite()
				if err != nil {
					s.logFunc("%s close write. err: %s", head, SwallowErrClosed(err))
				}
			}
		}
		cancel()
	})
	return func() {
		stopf()
		<-exit
		cancel()
	}
}
