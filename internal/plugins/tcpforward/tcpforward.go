package tcpforward

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// Plugin is a transparent TCP stream forwarder.
//
// Product path (国内前置 + 美国家宽落地):
//
//	phone ──mierus──► front:listen  ──raw TCP──►  exit mita:port  ──► residential exit
//
// Multi-exit on one front: multiple listen ports, each with its own target
// (e.g. 10401→exitA, 10402→exitB). Bytes are not interpreted; mieru auth/egress
// happen end-to-end on the destination mita.
type Plugin struct {
	DataDir string

	mu      sync.Mutex
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running bool
	lastKey string // listen/target fingerprint — skip rebind when unchanged
}

type forwardRule struct {
	listenPort int
	target     string // host:port
}

func (p *Plugin) Name() string { return "tcp_forward" }

func (p *Plugin) Apply(ctx context.Context, cfg map[string]interface{}) error {
	_ = ctx
	rules, err := parseRules(cfg)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return fmt.Errorf("tcp_forward: no rules")
	}

	// Stable key for idempotent re-apply.
	parts := make([]string, 0, len(rules))
	for _, r := range rules {
		parts = append(parts, fmt.Sprintf("%d→%s", r.listenPort, r.target))
	}
	sort.Strings(parts)
	key := fmt.Sprintf("%v", parts)

	p.mu.Lock()
	if p.running && p.lastKey == key {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	// Stop previous listeners fully before rebinding (avoids "address already in use"
	// when panel rebuilds while agent still holds the old sockets).
	p.stop()

	runCtx, cancel := context.WithCancel(context.Background())
	p.mu.Lock()
	p.cancel = cancel
	p.running = true
	p.lastKey = key
	p.mu.Unlock()

	var firstErr error
	started := 0
	for _, r := range rules {
		ln, err := listenTCP(r.listenPort)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("tcp_forward listen %d: %w", r.listenPort, err)
			}
			log.Printf("[tcp_forward] listen %d failed: %v", r.listenPort, err)
			continue
		}
		started++
		port := r.listenPort
		target := r.target
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
						if op, ok := err.(*net.OpError); ok && op.Err != nil {
							return
						}
						continue
					}
				}
				setTCPKeepAlive(c)
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
		p.mu.Lock()
		p.running = false
		p.cancel = nil
		p.lastKey = ""
		p.mu.Unlock()
		return firstErr
	}
	if firstErr != nil {
		log.Printf("[tcp_forward] partial start: %v (ok=%d)", firstErr, started)
	}
	return nil
}

func (p *Plugin) stop() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.running = false
	p.lastKey = ""
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Wait for accept loops + in-flight copies to exit and release ports.
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		log.Printf("[tcp_forward] stop wait timed out; continuing rebind")
	}
	// Brief grace for kernel to free TIME_WAIT / closed sockets.
	time.Sleep(150 * time.Millisecond)
}

func listenTCP(port int) (net.Listener, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, c syscall.RawConn) error {
			var opErr error
			err := c.Control(func(fd uintptr) {
				// Allow quick rebind after rebuild / agent restart.
				opErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
			})
			if err != nil {
				return err
			}
			return opErr
		},
	}
	return lc.Listen(context.Background(), "tcp", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
}

func setTCPKeepAlive(c net.Conn) {
	if tc, ok := c.(*net.TCPConn); ok {
		_ = tc.SetKeepAlive(true)
		_ = tc.SetKeepAlivePeriod(30 * time.Second)
	}
}

func (p *Plugin) handle(ctx context.Context, client net.Conn, target string) {
	defer client.Close()
	// Only bound the dial phase — once connected, keep the stream open for
	// long-lived mieru sessions (TK / streaming). Do not re-arm absolute deadlines.
	_ = client.SetDeadline(time.Now().Add(30 * time.Second))
	dialer := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	upstream, err := dialer.DialContext(ctx, "tcp", target)
	if err != nil {
		log.Printf("[tcp_forward] dial %s: %v", target, err)
		return
	}
	defer upstream.Close()
	setTCPKeepAlive(upstream)
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})

	var wg sync.WaitGroup
	wg.Add(2)
	copyFn := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
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

// parseRules accepts either:
//
//	rules: [{listen_port, target_host, target_port}, ...]   // multi-exit
//	or legacy single: listen_port + target_host + target_port
//	  (optional port_min/port_max only when no listen_port; wide pools collapse)
func parseRules(cfg map[string]interface{}) ([]forwardRule, error) {
	if raw, ok := cfg["rules"]; ok && raw != nil {
		list, ok := raw.([]interface{})
		if !ok {
			// already typed from non-JSON path
			if typed, ok2 := raw.([]map[string]interface{}); ok2 {
				out := make([]forwardRule, 0, len(typed))
				seen := map[int]bool{}
				for _, m := range typed {
					r, err := ruleFromMap(m)
					if err != nil {
						return nil, err
					}
					if seen[r.listenPort] {
						continue
					}
					seen[r.listenPort] = true
					out = append(out, r)
				}
				return out, nil
			}
			return nil, fmt.Errorf("tcp_forward: rules must be an array")
		}
		out := make([]forwardRule, 0, len(list))
		seen := map[int]bool{}
		for i, item := range list {
			m, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("tcp_forward: rules[%d] invalid", i)
			}
			r, err := ruleFromMap(m)
			if err != nil {
				return nil, fmt.Errorf("tcp_forward: rules[%d]: %w", i, err)
			}
			if seen[r.listenPort] {
				continue
			}
			seen[r.listenPort] = true
			out = append(out, r)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("tcp_forward: empty rules")
		}
		return out, nil
	}

	// Legacy single-target config.
	targetHost, _ := cfg["target_host"].(string)
	targetHost = trim(targetHost)
	targetPort := toInt(cfg["target_port"])
	if targetHost == "" || targetPort <= 0 {
		return nil, fmt.Errorf("tcp_forward: target_host/target_port required")
	}
	target := net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
	listenPort := toInt(cfg["listen_port"])
	pmin := toInt(cfg["port_min"])
	pmax := toInt(cfg["port_max"])
	ports := collectPorts(listenPort, pmin, pmax)
	if len(ports) == 0 {
		return nil, fmt.Errorf("tcp_forward: no listen port")
	}
	out := make([]forwardRule, 0, len(ports))
	for _, port := range ports {
		out = append(out, forwardRule{listenPort: port, target: target})
	}
	return out, nil
}

func ruleFromMap(m map[string]interface{}) (forwardRule, error) {
	listen := toInt(m["listen_port"])
	host, _ := m["target_host"].(string)
	host = trim(host)
	tport := toInt(m["target_port"])
	if listen <= 0 {
		return forwardRule{}, fmt.Errorf("listen_port required")
	}
	if host == "" || tport <= 0 {
		return forwardRule{}, fmt.Errorf("target_host/target_port required")
	}
	return forwardRule{
		listenPort: listen,
		target:     net.JoinHostPort(host, strconv.Itoa(tport)),
	}, nil
}

// collectPorts: for legacy single-target configs. A wide port_min/port_max
// (e.g. 10401-10499) is treated as operator pool metadata and must NOT open
// dozens of listeners (causes bind storms + rebind races).
func collectPorts(listen, pmin, pmax int) []int {
	if listen > 0 {
		return []int{listen}
	}
	if pmin > 0 && pmax >= pmin {
		if pmax-pmin > 7 {
			return []int{pmin}
		}
		out := make([]int, 0, pmax-pmin+1)
		for p := pmin; p <= pmax; p++ {
			out = append(out, p)
		}
		return out
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
