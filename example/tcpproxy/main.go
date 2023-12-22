package main

import (
	"context"
	"fmt"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/xiaotushaoxia/netserver"
)

var stdoutMutex sync.Mutex

type bl struct {
}

func (bb *bl) Printf(s string, args ...any) {
	stdoutMutex.Lock()
	defer stdoutMutex.Unlock()
	fmt.Printf(s+"\n", args...)
}

func fmtLogFunc(s string, a ...any) {
	stdoutMutex.Lock()
	defer stdoutMutex.Unlock()
	fmt.Printf(s+"\n", a...)
}

func main() {
	go func() {
		err := http.ListenAndServe("127.0.0.1:7575", nil)
		fmt.Println(err)
	}()

	m, err := NewProxy("127.0.0.1:5454", &bl{})
	if err != nil {
		panic(err)
	}

	server := netserver.New(m.newConn,
		netserver.WithLogFunc(fmtLogFunc, true),
		netserver.WithCloseClientMode(netserver.CloseClientByCancelCtx),
		netserver.WithCloseClientTimeout(time.Second),
		netserver.WithCloseWriteWhenShuttingDown(true),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err = server.RunTCP(ctx, ":5555")
	fmt.Println(err)
	fmt.Println(ctx.Err())
}
