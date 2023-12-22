package netserver

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"reflect"
	"time"

	"github.com/xiaotushaoxia/errx"
)

func IsErrClosed(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

// ============= NetServer =============
func (s *NetServer) closeListener(l net.Listener) {
	closeErr := l.Close()
	if closeErr != nil && !IsErrClosed(closeErr) {
		s.logFunc("close listener err: %s", closeErr)
	}
}

func (s *NetServer) stop(cancel context.CancelFunc) {
	s.logFunc(string(s.closeClientMode))
	defer s.logFunc("wait all conns exit")
	if s.closeClientMode == CloseClientByCancelCtx {
		cancel()
		return
	}

	s.closeClientConns(false) // 这里关闭会等待onShuttingDown调用
}

func (s *NetServer) waitClientExit() {
	tctx, tcancel := context.WithTimeout(context.Background(), s.closeClientTimeout)
	defer tcancel()
	if pollCheck(tctx, s.noActiveConn, s.showActiveConn) != nil {
		s.logFunc("wait for %s, wont wait anymore, %d conns not exit", s.closeClientTimeout, s.activeConn.Load())
		if s.closeClientMode != CloseClientByCancelCtx {
			s.logFunc("cancel ctx and wait %s,  but not all clients, close conn force", s.closeClientTimeout)
			s.closeClientConns(true) // 这里关闭会等待直接close conn
		}
	} else {
		s.logFunc("all conns are exited")
	}
}

func (s *NetServer) noActiveConn() bool {
	return s.activeConn.Load() == 0
}

func (s *NetServer) showActiveConn() {
	s.logFunc("ShowActiveConn ===========")
	switch s.activeConn.Load() {
	case 1:
		s.logFunc("showActiveConn: %d active conn is running", s.activeConn.Load())
	case 0:
		return
	default:
		s.logFunc("showActiveConn: %d active conns are running", s.activeConn.Load())
	}

	s.conns.Range(func(id uint64, conn net.Conn) bool {
		s.logFunc("showActiveConn: %s is active", getHead(id, conn))
		return true
	})
}

func (s *NetServer) setCtxCancel(ctx context.Context, cancel context.CancelFunc) {
	s.m.Lock()
	defer s.m.Unlock()
	s.cancel = cancel
	s.ctx = ctx
}

func (s *NetServer) closeClientConns(force bool) {
	s.conns.Range(func(_id uint64, _conn net.Conn) bool {
		go func(id uint64, conn net.Conn) {
			h := getHead(id, conn)
			if !force {
				value, ok := s.connShuttingDownCalled.Load(id)
				if !ok {
					s.logFunc("warn: %s not found shutting down ctx", h)
				} else {
					s.logFunc("%s wait ShuttingDownHandleFunc call", h)
					<-value.Done()
					s.logFunc("%s ShuttingDownHandleFunc called", h)
				}
			}
			s.connShuttingDownCalled.Delete(id)
			s.logFunc("%s close", h)
			if er := conn.Close(); er != nil && !IsErrClosed(er) {
				s.logFunc("%s close error: %s", h, er)
			}
		}(_id, _conn)
		return true
	})
}

func (s *NetServer) getCtxCancel() (context.Context, context.CancelFunc) {
	s.m.Lock()
	defer s.m.Unlock()
	return s.ctx, s.cancel
}

func (s *NetServer) wrapLogFunc(f func(string, ...any)) func(string, ...any) {
	if reflect.ValueOf(f).Pointer() == reflect.ValueOf(noopLogFunc).Pointer() {
		return noopLogFunc
	}
	return func(ss string, a ...any) {
		s.logFuncMutex.Lock()
		defer s.logFuncMutex.Unlock()
		ccc := time.Now().Format("2006-01-02 15:04:05.000")
		f(ccc+" [NetServer] "+ss, a...)
	}
}

// utils
const shutdownPollIntervalMax = 1000 * time.Millisecond

func pollCheck(ctx context.Context, check func() bool, print func()) error {
	pollIntervalBase := time.Millisecond * 10
	nextPollInterval := func() time.Duration {
		// Add 10% jitter.
		interval := pollIntervalBase + time.Duration(rand.Intn(int(pollIntervalBase/10)))
		// Double and clamp for next time.
		pollIntervalBase *= 2
		if pollIntervalBase > shutdownPollIntervalMax {
			pollIntervalBase = shutdownPollIntervalMax
		}
		return interval
	}
	timer := time.NewTimer(nextPollInterval())
	defer timer.Stop()
	for {
		if check() {
			return nil
		}
		if print != nil {
			print()
		}
		select {
		case <-ctx.Done():
			if check() {
				return nil
			}
			return ctx.Err()
		case <-timer.C:
			timer.Reset(nextPollInterval())
		}
	}
}

func isTemporaryAndGetNewDelay(oldDelay time.Duration, err error) (time.Duration, bool) {
	// 抄的go net/http 中Accept出错的处理
	if ne, ok := err.(net.Error); ok && ne.Temporary() {
		if oldDelay == 0 {
			oldDelay = 5 * time.Millisecond
		} else {
			oldDelay *= 2
		}
		if maxDelay := 1 * time.Second; oldDelay > maxDelay {
			oldDelay = maxDelay
		}
		return oldDelay, true
	}
	return 0, false
}

func failedTo(err error, op string, args ...any) error {
	if err == nil {
		return nil
	}
	return errx.Wrapf(err, fmt.Sprintf("failed to "+op, args...))
}

func noopLogFunc(string, ...any) {
	return
}

type closeReader interface {
	CloseRead() error
}

type closeWriter interface {
	CloseWrite() error
}

var noopFunc = func() {
	return
}

var closedChan = make(chan struct{})

func init() {
	close(closedChan)
}

func getHead(id uint64, conn net.Conn) string {
	return fmt.Sprintf("conn %d[%s]:", id, conn.RemoteAddr())
}

type noopLocker struct {
}

func (n noopLocker) Lock() {
	return
}

func (n noopLocker) Unlock() {
	return
}

type retryDelay struct {
	maxDelay  time.Duration
	currDelay time.Duration
	initDelay time.Duration
}

func (d *retryDelay) next() time.Duration {
	var rev = d.currDelay
	if rev == 0 {
		if d.initDelay == 0 {
			d.initDelay = 5 * time.Millisecond
		}
		rev = d.initDelay
	} else {
		rev *= 2
	}
	if rev > d.maxDelay {
		rev = d.maxDelay
	}
	return rev
}

func (d *retryDelay) reset() {
	d.currDelay = 0
}

func (d *retryDelay) delay(ctx context.Context) (interrupted bool) {
	d.currDelay = d.next()
	if ctx == nil || ctx.Done() == nil {
		time.Sleep(d.currDelay)
		return false
	}
	t := time.NewTimer(d.currDelay)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}

type ignoreTemporaryErrorListener struct {
	l      net.Listener
	connCh chan net.Conn
	errCh  chan error
}

func newIgnoreTemporaryErrorListener(l net.Listener) *ignoreTemporaryErrorListener {
	a := ignoreTemporaryErrorListener{l: l, connCh: make(chan net.Conn, 1), errCh: make(chan error, 1)}
	return &a
}

func (l *ignoreTemporaryErrorListener) back(ctx context.Context) {
	delayer := retryDelay{maxDelay: time.Second, initDelay: time.Millisecond * 5}
	for {
		conn, er := l.l.Accept()
		if er == nil {
			delayer.reset()
			l.connCh <- conn
			continue
		}
		select {
		case <-ctx.Done():
			// ctx.Done() may eq to  errors.Is(er, net.IsErrClosed)
			l.errCh <- nil
			return
		default:
			fmt.Printf("accept error: %v\n", er)
			if !IsTemporary(er) {
				fmt.Printf("not temporary error exit\n")
				l.errCh <- er
				return
			}
			fmt.Printf("accept temporary error: retrying in %v\n", delayer.next())
			delayer.delay(ctx)
		}
	}

}

// IsTemporary
// 本来应该是if ne, ok := er.(net.Error); ok && ne.Temporary()，但是Deprecated的提醒有点烦
// Temporary虽然Deprecated了，但是net.http还在用，而且确实有error是Temporary的，比如too many open file
func IsTemporary(err error) bool {
	if ne, ok := err.(interface{ Temporary() bool }); !ok && !ne.Temporary() {
		return false
	}
	return true
}

func SwallowErrClosed(err error) error {
	if err == nil {
		return nil
	}
	if IsErrClosed(err) {
		return nil
	}
	return err
}
