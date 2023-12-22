package netserver

import (
	"context"
	"fmt"
	"io"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	server := New(echo, WithLogFunc(func(s string, a ...any) {
		fmt.Printf(s, a...)
		fmt.Printf("\n")
	}, true))
	fmt.Println(server.Close())
	go func() {
		time.Sleep(time.Second * 3)
		fmt.Println(server.Close())
	}()

	fmt.Println(server.ServeTCP("127.0.0.1:5555"))
}

func TestNewAcceptError(t *testing.T) {
	server := New(echo, WithLogFunc(func(s string, a ...any) {
		fmt.Printf(s, a...)
		fmt.Printf("\n")
	}, true))

	listen, err := net.Listen("tcp", "127.0.0.1:5555")
	if err != nil {
		t.Fatalf(err.Error())
	}

	ll := &acceptNListener{0, listen}
	err = server.Serve(ll)
	fmt.Println(err)
}

type acceptNListener struct {
	n int
	net.Listener
}

func (al *acceptNListener) Accept() (net.Conn, error) {
	al.n++
	fmt.Println(al.n)
	if al.n > 5 {
		return nil, fmt.Errorf("模拟accept错误")
	}
	return al.Listener.Accept()
}

func echo(ctx context.Context, conn net.Conn) {
	written, err := io.Copy(conn, conn)
	fmt.Printf("copy %d err:%v \n", written, err)
}

func cc(f func(string2 string, vs ...any)) {
	if reflect.ValueOf(f).Pointer() == reflect.ValueOf(noopLogFunc).Pointer() {
		fmt.Println("eq")
	} else {
		fmt.Println("not eq")
	}
}

func TestFF(t *testing.T) {
	cc(noopLogFunc)
}
