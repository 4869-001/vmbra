// Package wsx 是一个极简 WebSocket 服务端实现，仅覆盖管理工作站所需的帧类型。
package wsx

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// 帧操作码
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// ErrClosed 表示连接已关闭。
var ErrClosed = errors.New("websocket 连接已关闭")

// Conn 是一条已升级的 WebSocket 连接。
type Conn struct {
	conn   net.Conn
	br     *bufio.Reader
	wmu    sync.Mutex
	closed bool
}

// Upgrade 完成 HTTP → WebSocket 升级。
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("不是 WebSocket 升级请求")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if key == "" {
		return nil, errors.New("缺少 Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("当前连接不支持升级")
	}
	netConn, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}

	sum := sha1.Sum([]byte(key + wsGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	if _, err := netConn.Write([]byte(resp)); err != nil {
		netConn.Close()
		return nil, err
	}
	_ = netConn.SetDeadline(time.Time{})
	return &Conn{conn: netConn, br: brw.Reader}, nil
}

// ReadMessage 读取一条完整消息（自动处理分片、ping/pong 与关闭帧）。
func (c *Conn) ReadMessage() (byte, []byte, error) {
	var (
		opcode  byte
		payload []byte
		started bool
	)
	for {
		fin, op, data, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			_ = c.WriteControl(OpPong, data)
			continue
		case OpPong:
			continue
		case OpClose:
			_ = c.WriteControl(OpClose, nil)
			c.Close()
			return 0, nil, ErrClosed
		}
		if op != OpContinuation {
			opcode = op
			started = true
		} else if !started {
			continue
		}
		payload = append(payload, data...)
		if fin {
			return opcode, payload, nil
		}
		if len(payload) > 32<<20 {
			return 0, nil, errors.New("消息过大")
		}
	}
}

func (c *Conn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err = io.ReadFull(c.br, hdr[:]); err != nil {
		return
	}
	fin = hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		err = errors.New("不支持的扩展位")
		return
	}
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7f)

	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return
		}
		length = int64(binary.BigEndian.Uint64(ext[:]))
	}
	if length < 0 || length > 32<<20 {
		err = errors.New("帧长度非法")
		return
	}
	var mask [4]byte
	if masked {
		if _, err = io.ReadFull(c.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

func (c *Conn) writeFrame(opcode byte, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if c.closed {
		return ErrClosed
	}
	header := make([]byte, 0, 10)
	header = append(header, 0x80|opcode)
	n := len(data)
	switch {
	case n < 126:
		header = append(header, byte(n))
	case n <= 65535:
		header = append(header, 126, byte(n>>8), byte(n))
	default:
		header = append(header, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		header = append(header, ext[:]...)
	}
	if _, err := c.conn.Write(header); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err := c.conn.Write(data)
	return err
}

// WriteText 发送文本消息。
func (c *Conn) WriteText(s string) error { return c.writeFrame(OpText, []byte(s)) }

// WriteBinary 发送二进制消息。
func (c *Conn) WriteBinary(b []byte) error { return c.writeFrame(OpBinary, b) }

// WriteJSON 发送 JSON 文本（调用方负责序列化）。
func (c *Conn) WriteControl(opcode byte, data []byte) error { return c.writeFrame(opcode, data) }

// Close 关闭连接。
func (c *Conn) Close() error {
	c.wmu.Lock()
	if c.closed {
		c.wmu.Unlock()
		return nil
	}
	c.closed = true
	c.wmu.Unlock()
	return c.conn.Close()
}

// SetReadDeadline 设置读超时（用于心跳超时回收）。
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// RemoteAddr 返回对端地址。
func (c *Conn) RemoteAddr() string { return c.conn.RemoteAddr().String() }

// JSON 序列化辅助，避免调用方重复引入 encoding/json 逻辑。
func JSON(parts ...string) string {
	return fmt.Sprintf("{%s}", strings.Join(parts, ","))
}
