package main

import (
	"context"
	"fmt"
	"io"
	"net"
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

	server := netserver.New(echo,
		netserver.WithLogFunc(fmtLogFunc, true),
		netserver.WithCloseClientTimeout(time.Second*10),
		netserver.WithHandleFuncCancelTimeout(time.Second*10),
		netserver.WithForceCloseClientIfTimeout(true),
	)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	err := server.RunTCP(ctx, ":5555")
	fmt.Println(err)
	fmt.Println(ctx.Err())
}

func echo(ctx context.Context, conn net.Conn) {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	written, err := io.Copy(conn, conn)
	fmtLogFunc("echo done, copy %d, err: %s", written, err)
}
