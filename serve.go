package netserver

import (
	"context"
	"fmt"
	"net"
)

var ErrServerNotServing = fmt.Errorf("server not serving")

const serveKey = "serve"

func (s *NetServer) Serve(l net.Listener) error {
	return s.single(func() error {
		return s.RunListener(s.ctx, l)
	})
}

func (s *NetServer) ServeTLS(addr, certFile, keyFile string) error {
	return s.single(func() error {
		return s.RunTLS(s.ctx, addr, certFile, keyFile)
	})
}

func (s *NetServer) ServeTCP(addr string) error {
	return s.single(func() error {
		return s.RunTCP(s.ctx, addr)
	})
}

func (s *NetServer) ServeFD(fd int) error {
	return s.single(func() error {
		return s.RunFD(s.ctx, fd)
	})
}

func (s *NetServer) ServeUnix(file string) error {
	return s.single(func() error {
		return s.RunUnix(s.ctx, file)
	})
}

func (s *NetServer) single(f func() error) error {
	v, _, _ := s.sfg.Do(serveKey, func() (any, error) {
		ctx, cancel := context.WithCancel(context.Background())
		s.setCtxCancel(ctx, cancel)
		defer func() {
			s.setCtxCancel(nil, nil)
		}()
		return f(), nil
	})
	return v.(error)
}

// Close 直接返回
func (s *NetServer) Close() error {
	_, cancelFunc := s.getCtxCancel()
	if cancelFunc == nil {
		return ErrServerNotServing
	}
	s.cancel()
	return nil
}

// Shutdown 会一直等到ctx.Done或者Serve函数退出才返回
func (s *NetServer) Shutdown(ctx context.Context) error {
	if err := s.Close(); err != nil {
		return err
	}

	if err := pollCheck(ctx, s.noActiveConn, nil); err != nil {
		return err
	}
	return nil
}
