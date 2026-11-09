package forward

import (
	"io"
	"net"
	"strconv"
	"testing"
	"time"
)

func itoa(i int) string { return strconv.Itoa(i) }

// freePort 申请一个空闲端口（关闭后返回，存在极小的竞争窗口，测试可接受）。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// echoServer 启动一个回显服务，返回端口与关闭函数。
func echoServer(t *testing.T) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { _ = ln.Close() }
}

func TestForwardRelay(t *testing.T) {
	guestPort, stopGuest := echoServer(t)
	defer stopGuest()
	hostPort := freePort(t)

	m := New()
	defer m.StopAll()
	if err := m.Start("f1", hostPort, "127.0.0.1", guestPort); err != nil {
		t.Fatalf("启动转发失败：%v", err)
	}
	st := m.Stats("f1")
	if !st.Listening || st.Addr == "" {
		t.Fatalf("状态异常：%+v", st)
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", itoa(hostPort)), 2*time.Second)
	if err != nil {
		t.Fatalf("连接转发端口失败：%v", err)
	}
	payload := []byte("ping-through-forward")
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("读取回显失败：%v", err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("回显不一致：%q", string(buf))
	}
	_ = conn.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		st = m.Stats("f1")
		if st.BytesUp >= int64(len(payload)) && st.BytesDown >= int64(len(payload)) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if st.Connections < 1 || st.BytesUp < int64(len(payload)) || st.BytesDown < int64(len(payload)) {
		t.Fatalf("统计未更新：%+v", st)
	}
}

func TestForwardPortConflict(t *testing.T) {
	port := freePort(t)
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	m := New()
	if err := m.Start("f2", port, "127.0.0.1", 80); err == nil {
		t.Fatal("端口被占用时应返回错误")
	}
	st := m.Stats("f2")
	if st.Listening || st.Error == "" {
		t.Fatalf("冲突状态未记录：%+v", st)
	}
}

func TestForwardStopReleasesPort(t *testing.T) {
	port := freePort(t)
	m := New()
	if err := m.Start("f3", port, "127.0.0.1", 80); err != nil {
		t.Fatal(err)
	}
	m.Stop("f3")
	if m.Stats("f3").Listening {
		t.Fatal("停止后不应处于监听状态")
	}
	if m.Running("f3") {
		t.Fatal("停止后 Running 应为 false")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatalf("端口未释放：%v", err)
	}
	_ = ln.Close()
}

func TestForwardInvalidPort(t *testing.T) {
	m := New()
	if err := m.Start("f4", 0, "127.0.0.1", 80); err == nil {
		t.Fatal("非法端口应被拒绝")
	}
	if err := m.Start("f5", 8080, "127.0.0.1", 70000); err == nil {
		t.Fatal("非法目标端口应被拒绝")
	}
}
