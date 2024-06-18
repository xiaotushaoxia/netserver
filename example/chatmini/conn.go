package main

import (
	"context"
	"fmt"
	"net"

	"sync"
	"sync/atomic"
	"time"

	"github.com/xiaotushaoxia/synccollections"
)

type Manger struct {
	sync.Mutex
	conns synccollections.Map[uint64, net.Conn]
	seq   atomic.Uint64
}

func (m *Manger) sayBye(ctx context.Context, conn net.Conn) {
	time.Sleep(time.Second * 2)
	_, err := conn.Write([]byte("server is close\n"))
	fmt.Println("ctx done write bye", err)
}

func (m *Manger) newConn(ctx context.Context, conn net.Conn) {
	id := m.seq.Add(1)
	m.conns.Store(id, conn)
	m.sendAll(id, []byte("i am online\n"))

	defer func() {
		select {
		case <-ctx.Done():
			fmt.Println("ctx done. server close")
		default:
			fmt.Println("client close")
			m.conns.Delete(id)
			m.sendAll(id, []byte("i quit\n"))
		}
		fmt.Println(id, "exit")
	}()

	var bs = make([]byte, 1024)
	for {
		n, err := conn.Read(bs)
		if err != nil {
			fmt.Println(id, "read error", err)
			return
		}
		m.sendAll(id, append(bs[:n], '\n'))
	}
}

func (m *Manger) sendAll(from uint64, msg []byte) {
	fmt.Printf("%d:%s", from, string(msg))

	msg = append([]byte(fmt.Sprintf("%d said:", from)), msg...)
	var needDelete []uint64
	m.conns.Range(func(key uint64, conn net.Conn) (shouldContinue bool) {
		if key == from {
			return true
		}
		_, err := conn.Write(msg)
		if err != nil {
			fmt.Println("write to ", key, "failed", err, "close. close err:", conn.Close())
			needDelete = append(needDelete, key)
		}
		return true
	})

	for _, u := range needDelete {
		m.conns.Delete(u)
	}
}
