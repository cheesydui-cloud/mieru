package socksin

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Plugin runs an in-process SOCKS5 server (user/pass) with optional upstream SOCKS5 (mieru).
type Plugin struct {
	DataDir string

	mu       sync.Mutex
	listener net.Listener
	cancel   context.CancelFunc
	users    map[string]string
	upstream string
	listen   string
}

func (p *Plugin) Name() string { return "socks_in" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	if err := os.MkdirAll(p.DataDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(p.DataDir, "socks-in.json")
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}

	port := toInt(cfg["listen_port"])
	if port <= 0 {
		port = 1080
	}
	users := map[string]string{}
	if u, ok := cfg["users"].([]interface{}); ok {
		for _, it := range u {
			m, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := m["username"].(string)
			if name == "" {
				name, _ = m["name"].(string)
			}
			pass, _ := m["password"].(string)
			enabled := true
			if e, ok := m["enabled"].(bool); ok {
				enabled = e
			}
			if name != "" && pass != "" && enabled {
				users[name] = pass
			}
		}
	}

	upHost, _ := cfg["upstream_host"].(string)
	upPort := toInt(cfg["upstream_port"])
	upstream := ""
	if upHost != "" && upPort > 0 {
		upstream = net.JoinHostPort(upHost, strconv.Itoa(upPort))
	}

	listenAddr := net.JoinHostPort("0.0.0.0", strconv.Itoa(port))
	return p.restart(listenAddr, users, upstream)
}

func (p *Plugin) restart(listen string, users map[string]string, upstream string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	if p.listener != nil {
		_ = p.listener.Close()
		p.listener = nil
	}

	p.users = users
	p.upstream = upstream
	p.listen = listen

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("socks_in listen %s: %w", listen, err)
	}
	p.listener = ln
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	log.Printf("[socks_in] listening %s users=%d upstream=%q", listen, len(users), upstream)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					if ne, ok := err.(net.Error); ok && ne.Temporary() {
						time.Sleep(50 * time.Millisecond)
						continue
					}
					return
				}
			}
			go p.handle(conn)
		}
	}()
	return nil
}

func (p *Plugin) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	buf := make([]byte, 258)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	nMethods := int(buf[1])
	if nMethods <= 0 {
		return
	}
	if _, err := io.ReadFull(conn, buf[:nMethods]); err != nil {
		return
	}
	if _, err := conn.Write([]byte{0x05, 0x02}); err != nil {
		return
	}

	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x01 {
		return
	}
	ulen := int(buf[1])
	if _, err := io.ReadFull(conn, buf[:ulen+1]); err != nil {
		return
	}
	user := string(buf[:ulen])
	plen := int(buf[ulen])
	if _, err := io.ReadFull(conn, buf[:plen]); err != nil {
		return
	}
	pass := string(buf[:plen])

	p.mu.Lock()
	want, ok := p.users[user]
	up := p.upstream
	p.mu.Unlock()
	if !ok || want != pass {
		_, _ = conn.Write([]byte{0x01, 0x01})
		return
	}
	if _, err := conn.Write([]byte{0x01, 0x00}); err != nil {
		return
	}

	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		return
	}
	if buf[0] != 0x05 || buf[1] != 0x01 {
		p.reply(conn, 0x07)
		return
	}
	var host string
	switch buf[3] {
	case 0x01:
		if _, err := io.ReadFull(conn, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03:
		if _, err := io.ReadFull(conn, buf[:1]); err != nil {
			return
		}
		l := int(buf[0])
		if _, err := io.ReadFull(conn, buf[:l]); err != nil {
			return
		}
		host = string(buf[:l])
	case 0x04:
		if _, err := io.ReadFull(conn, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		p.reply(conn, 0x08)
		return
	}
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(buf[:2])
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))
	_ = conn.SetDeadline(time.Time{})

	var remote net.Conn
	var err error
	if up != "" {
		// re-use client credentials on next-hop SOCKS (entry → relay)
		remote, err = dialViaSocks5(up, target, user, pass, 15*time.Second)
	} else {
		remote, err = net.DialTimeout("tcp", target, 15*time.Second)
	}
	if err != nil {
		p.reply(conn, 0x05)
		return
	}
	defer remote.Close()
	p.reply(conn, 0x00)

	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(remote, conn); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, remote); errc <- struct{}{} }()
	<-errc
}

func (p *Plugin) reply(conn net.Conn, rep byte) {
	_, _ = conn.Write([]byte{0x05, rep, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
}

func dialViaSocks5(proxyAddr, target, user, pass string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxyAddr, timeout)
	if err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	// offer no-auth and user/pass
	if _, err := conn.Write([]byte{0x05, 0x02, 0x00, 0x02}); err != nil {
		conn.Close()
		return nil, err
	}
	buf := make([]byte, 512)
	if _, err := io.ReadFull(conn, buf[:2]); err != nil {
		conn.Close()
		return nil, err
	}
	if buf[0] != 0x05 {
		conn.Close()
		return nil, fmt.Errorf("upstream not socks5")
	}
	switch buf[1] {
	case 0x00:
		// no auth
	case 0x02:
		if user == "" {
			conn.Close()
			return nil, fmt.Errorf("upstream requires auth")
		}
		req := []byte{0x01, byte(len(user))}
		req = append(req, user...)
		req = append(req, byte(len(pass)))
		req = append(req, pass...)
		if _, err := conn.Write(req); err != nil {
			conn.Close()
			return nil, err
		}
		if _, err := io.ReadFull(conn, buf[:2]); err != nil {
			conn.Close()
			return nil, err
		}
		if buf[1] != 0x00 {
			conn.Close()
			return nil, fmt.Errorf("upstream auth failed")
		}
	default:
		conn.Close()
		return nil, fmt.Errorf("upstream socks method rejected %d", buf[1])
	}
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		conn.Close()
		return nil, err
	}
	port, _ := strconv.Atoi(portStr)
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}
	req = append(req, host...)
	var pb [2]byte
	binary.BigEndian.PutUint16(pb[:], uint16(port))
	req = append(req, pb[:]...)
	if _, err := conn.Write(req); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := io.ReadFull(conn, buf[:4]); err != nil {
		conn.Close()
		return nil, err
	}
	if buf[1] != 0x00 {
		conn.Close()
		return nil, fmt.Errorf("upstream connect failed rep=%d", buf[1])
	}
	switch buf[3] {
	case 0x01:
		_, _ = io.ReadFull(conn, buf[:6])
	case 0x03:
		_, _ = io.ReadFull(conn, buf[:1])
		l := int(buf[0])
		_, _ = io.ReadFull(conn, buf[:l+2])
	case 0x04:
		_, _ = io.ReadFull(conn, buf[:18])
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case int:
		return t
	case int64:
		return int(t)
	case string:
		n, _ := strconv.Atoi(t)
		return n
	default:
		return 0
	}
}
