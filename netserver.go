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

type CloseClientMode string
type HandleFunc func(context.Context, net.Conn)
type LogFunc func(string, ...any)

const (
	CloseClientByCancelCtx CloseClientMode = "CloseClientByCancelCtx"
	CloseClientByCloseConn CloseClientMode = "CloseClientByCloseConn"
)

type Options func(*NetServer)

func WithCloseClientMode(m CloseClientMode) func(*NetServer) {
	return func(s *NetServer) {
		s.closeClientMode = m
	}
}

func WithLogFunc(f LogFunc, syncLog bool) func(*NetServer) {
	return func(s *NetServer) {
		if syncLog {
			s.logFuncMutex = &sync.Mutex{}
		}
		s.logFunc = s.wrapLogFunc(f)
	}
}

func WithCloseClientTimeout(duration time.Duration) func(*NetServer) {
	return func(s *NetServer) {
		s.closeClientTimeout = duration
	}
}

func WithCloseWriteWhenShuttingDown(cw bool) func(*NetServer) {
	return func(s *NetServer) {
		s.closeWriteWhenShuttingDown = cw
	}
}

func WithShuttingDownHandleFunc(hf HandleFunc) func(*NetServer) {
	return func(s *NetServer) {
		s.ShuttingDownHandleFunc = hf
	}
}

func New(hf HandleFunc, opts ...Options) *NetServer {
	s := &NetServer{
		HandleFunc:                 hf,
		logFunc:                    noopLogFunc,
		closeClientMode:            CloseClientByCloseConn,
		closeClientTimeout:         time.Second * 10,
		closeWriteWhenShuttingDown: true,
		logFuncMutex:               noopLocker{},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

type NetServer struct {
	// options
	HandleFunc             HandleFunc
	ShuttingDownHandleFunc HandleFunc

	logFunc         LogFunc         //日志函数
	closeClientMode CloseClientMode //当服务器关闭的时候如何关闭已有连接

	closeClientTimeout time.Duration //关闭已有连接后等待多久等HandleFunc退出

	//当服务器关闭的时候，是不是要先关闭read。主动关闭方close read应该是比较正确的行为
	closeWriteWhenShuttingDown bool

	// 内部变量  维护当前连接数
	conns                  synccollections.Map[uint64, net.Conn]
	connShuttingDownCalled synccollections.Map[uint64, context.Context]

	count      atomic.Uint64 // 其实可以不用atomic 但是方便用count.Add(1)替换count++作为参数，少写一行
	activeConn atomic.Uint64

	sfg singleflight.Group // 保证只serve一次

	logFuncMutex sync.Locker // 让logFunc的输出不混在一起

	// 调用serve方法时有效
	m      sync.Mutex //保护 cancel ctx
	ctx    context.Context
	cancel context.CancelFunc

	wg sync.WaitGroup // 防止服务端关闭的时候ShuttingDownHandleFunc还没调用  就直接调用了 conns.Close
}
