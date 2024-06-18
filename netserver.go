package netserver

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xiaotushaoxia/synccollections"
	"golang.org/x/sync/singleflight"
)

type HandleFunc func(context.Context, net.Conn)
type LogFunc func(string, ...any)

type Options func(*NetServer)

func WithLogFunc(f LogFunc, syncLog bool) func(*NetServer) {
	return func(s *NetServer) {
		if syncLog {
			s.logFuncMutex = &sync.Mutex{}
		}
		s.logFunc = s.wrapLogFunc(f)
	}
}

func WithHandleFuncCancelTimeout(duration time.Duration) func(*NetServer) {
	return func(s *NetServer) {
		s.handleFuncCancelTimeout = duration
	}
}

func WithCloseClientTimeout(duration time.Duration) func(*NetServer) {
	return func(s *NetServer) {
		s.closeClientTimeout = duration
	}
}

func WithForceCloseClientIfTimeout(forceClose bool) func(*NetServer) {
	return func(s *NetServer) {
		s.forceCloseClientIfTimeout = forceClose
	}
}

func New(hf HandleFunc, opts ...Options) *NetServer {
	s := &NetServer{
		HandleFunc:              hf,
		logFunc:                 noopLogFunc,
		handleFuncCancelTimeout: time.Second * 10,
		closeClientTimeout:      time.Second * 10,
		logFuncMutex:            noopLocker{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type NetServer struct {
	// options
	HandleFunc HandleFunc

	logFuncMutex sync.Locker // 让logFunc的输出不混在一起
	logFunc      LogFunc     // 日志函数

	handleFuncCancelTimeout   time.Duration // 客户端的ctx取消后等待多久等HandleFunc退出
	closeClientTimeout        time.Duration // 客户端调用CLose后等待多久等HandleFunc退出
	forceCloseClientIfTimeout bool          // 等待handleFuncCancelTimeout后 如客户端的HandleFunc还没退出 就调用conn.Close

	// 内部变量  维护当前连接数
	conns      synccollections.Map[uint64, *ClientConn]
	count      atomic.Uint64 // 其实可以不用atomic 但是方便用count.Add(1)替换count++作为参数，少写一行
	activeConn atomic.Uint64

	sfg singleflight.Group // 保证只serve一次

	// 调用serve方法时有效
	m      sync.Mutex //保护 cancel ctx
	ctx    context.Context
	cancel context.CancelFunc
}

func (s *NetServer) Clients() []*ClientConn {
	var vs = make([]*ClientConn, 0, s.activeConn.Load())
	s.conns.Range(func(key uint64, value *ClientConn) (shouldContinue bool) {
		vs = append(vs, value)
		return true
	})
	return vs
}

func (s *NetServer) GetClient(cid uint64) (*ClientConn, bool) {
	return s.conns.Load(cid)
}

type ClientConn struct {
	Id        uint64
	StartTime time.Time
	Ctx       context.Context
	Cancel    context.CancelFunc
	Conn      net.Conn
}
