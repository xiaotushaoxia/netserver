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

func isErrClosed(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

// ============= NetServer =============
func (s *NetServer) closeListener(l net.Listener) {
	closeErr := l.Close()
	if closeErr != nil && !isErrClosed(closeErr) {
		s.logFunc("close listener err: %s", closeErr)
	}
}

func (s *NetServer) cleanUp() {
	s.waitClientExit(s.handleFuncCancelTimeout)
	if s.activeConn.Load() == 0 {
		return
	}
	if !s.forceCloseClientIfTimeout {
		s.logFunc("warn: ForceCloseClientIfTimeout disabled")
		return
	}
	s.logFunc("ForceCloseClientIfTimeout enabled. try Close Conn and wait")

	s.conns.Range(func(key uint64, value *ClientConn) (shouldContinue bool) {
		s.logFunc("close %s, err: %v", getName(value.Id, value.Conn), swallowErrClosed(value.Conn.Close()))
		return true
	})

	s.waitClientExit(s.closeClientTimeout)
}

func (s *NetServer) waitOneClientExit(cid uint64, timeout time.Duration) {
	tctx, tcancel := context.WithTimeout(context.Background(), timeout)
	defer tcancel()
	if pollCheck(tctx, s.isClientExitedFunc(cid), func() {
		s.logFunc("conn %d is alive", cid)
	}) == nil {
		return
	}
	s.logFunc("wait for %s, wont wait anymore, conn %d still alive", timeout, cid)
}

func (s *NetServer) waitClientExit(timeout time.Duration) {
	tctx, tcancel := context.WithTimeout(context.Background(), timeout)
	defer tcancel()
	if pollCheck(tctx, s.noActiveConn, s.showActiveConn) == nil {
		return
	}
	s.logFunc("wait for %s, wont wait anymore, %d conns still alive", timeout, s.activeConn.Load())
}

func (s *NetServer) noActiveConn() bool {
	return s.activeConn.Load() == 0
}

func (s *NetServer) isClientExitedFunc(cid uint64) func() bool {
	return func() bool {
		_, ok := s.conns.Load(cid)
		return !ok
	}
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

	s.conns.Range(func(id uint64, conn *ClientConn) bool {
		s.logFunc("showActiveConn: %s is active", getName(id, conn.Conn))
		return true
	})
}

func (s *NetServer) setCtxCancel(ctx context.Context, cancel context.CancelFunc) {
	s.m.Lock()
	defer s.m.Unlock()
	s.cancel = cancel
	s.ctx = ctx
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

func pollCheck(ctx context.Context, check func() bool, debugInfo func()) error {
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
		if debugInfo != nil {
			debugInfo()
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

func failedTo(err error, op string, args ...any) error {
	if err == nil {
		return nil
	}
	return errx.Wrapf(err, fmt.Sprintf("failed to "+op, args...))
}

func noopLogFunc(string, ...any) {
	return
}

var closedChan = make(chan struct{})

func init() {
	close(closedChan)
}

func getName(id uint64, conn net.Conn) string {
	return fmt.Sprintf("conn %d [%s]", id, conn.RemoteAddr())
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

// isTemporary
// 本来应该是if ne, ok := er.(net.Error); ok && ne.Temporary()，但是Deprecated的提醒有点烦
// Temporary虽然Deprecated了，但是net.http还在用，而且确实有error是Temporary的，比如too many open file
func isTemporary(err error) bool {
	if ne, ok := err.(interface{ Temporary() bool }); !ok && !ne.Temporary() {
		return false
	}
	return true
}

func swallowErrClosed(err error) error {
	if err == nil {
		return nil
	}
	if isErrClosed(err) {
		return nil
	}
	return err
}

func getCtxCauseErr(ctx context.Context) error {
	//// context.Cause(ctx) should eq to ctx.Err() or not nil. will fix in go1.22
	//// https://github.com/golang/go/issues/62582
	//return errx.WithMessage(ctx.Err(), "ctx done")
	if er := context.Cause(ctx); er != nil {
		return er
	}
	return ctx.Err()
}
