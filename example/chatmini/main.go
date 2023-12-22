package main

import (
	"context"
	"fmt"
	"net/http"

	"os/signal"
	"syscall"
	"time"

	_ "net/http/pprof"

	"github.com/xiaotushaoxia/netserver"
)

func fmtLogFunc(s string, a ...any) {
	fmt.Printf(s, a...)
	fmt.Printf("\n")
}

func main() {
	go func() {
		err := http.ListenAndServe("127.0.0.1:7575", nil)
		fmt.Println(err)
	}()

	m := Manger{}

	server := netserver.New(m.newConn,
		netserver.WithLogFunc(fmtLogFunc, true),
		netserver.WithCloseClientMode(netserver.CloseClientByCancelCtx),
		//netserver.WithCloseClientMode(netserver.CloseClientByCloseConn),
		netserver.WithCloseClientTimeout(time.Second),
		netserver.WithShuttingDownHandleFunc(m.sayBye),
		netserver.WithCloseWriteWhenShuttingDown(true),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err := server.RunTCP(ctx, ":5555")
	fmt.Println(err)
	fmt.Println(ctx.Err())
}
