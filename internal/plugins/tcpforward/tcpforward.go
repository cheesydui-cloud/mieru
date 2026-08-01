package tcpforward

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

// Plugin is a transparent TCP stream forwarder.
//
// Use case (panel multi-hop with official mieru client):
//
//	phone ──mierus──► front:listen  ──raw TCP──►  exit mita:port  ──► residential exit
//
// Bytes are not interpreted; mieru handshake/auth happens end-to-end on the exit mita.
// This is how a domestic front + US residential exit works when the client must speak mieru
// (socks5 entry is not acceptable).
type Plugin struct {
	DataDir string

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
}

func (p *Plugin) Name() string { return "tcp_forward" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	listenPort := toInt(cfg["listen_port"])
	pmin := toInt(cfg["port_min"])
	pmax := toInt(cfg["port_max"])
	targetHost, _ := cfg["target_host"].(string)
	targetPort := toInt(cfg["target_port"])
	targetHost = trim(targetHost)
	if targetHost == "" || targetPort <= 0 {
		return fmt.Errorf("tcp_forward: target_host/target_port required")
	}

	ports := collectPorts(listenPort, pmin, pmax)
	if len(ports) == 0 {
		return fmt.Errorf("tcp_forward: no listen port")
	}

	p.mu.Lock()
	// stop previous
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.mu.Unlock()
	p.wg.Wait()

	runCtx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.running = true
	p.mu.Unlock()

	target := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	var firstErr error
	started := 0
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tcp_forward listen %d: %w", port, err)
			}
			log.Printf("[tcp_forward] listen %d failed: %v", port, err)
			continue
		}
		started++
		port := port
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer ln.Close()
			log.Printf("[tcp_forward] %d → %s", port, target)
			go func() {
				<-runCtx.Done()
				_ = ln.Close()
			}()
			for {
				c, err := ln.Accept()
				if err != nil {
					select {
					case <-runCtx.Done():
						return
					default:
						time.Sleep(50 * time.Millisecond)
						// listener closed or fatal
						if op, ok := err.(*net.OpError); ok && op.Err != nil {
							return
						}
						continue
					}
				}
				p.wg.Add(1)
				go func(client net.Conn) {
					defer p.wg.Done()
					p.handle(runCtx, client, target)
				}(c)
			}
		}()
	}
	if started == 0 {
		cancel()
		return firstErr
	}
	if firstErr != nil {
		log.Printf("[tcp_forward] partial start: %v (ok=%d)", firstErr, started)
	}
	return nil
}

func (p *Plugin) handle(ctx context.Context, client net.Conn, target string) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Printf("[tcp_forward] dial %s: %v", target, err)
		return
	}
	defer upstream.Close()
	// clear handshake deadline — long-lived proxy streams
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})

	var wg sync.WaitGroup
	wg.Add(2)
	copyFn := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// unblock peer
		type closeWriter interface{ CloseWrite() error }
		if cw, ok := dst.(closeWriter); ok {
			_ = cw.CloseWrite()
		}
	}
	go copyFn(upstream, client)
	go copyFn(client, upstream)
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		_ = upstream.Close()
		<-done
	case <-done:
	}
}

func collectPorts(listen, pmin, pmax int) []int {
	if pmin > 0 && pmax >= pmin {
		// Cap range size to avoid opening hundreds of sockets by accident.
		if pmax-pmin > 64 {
			pmax = pmin + 64
		}
		out := make([]int, 0, pmax-pmin+1)
		for p := pmin; p <= pmax; p++ {
			out = append(out, p)
		}
		return out
	}
	if listen > 0 {
		return []int{listen}
	}
	return nil
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

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
