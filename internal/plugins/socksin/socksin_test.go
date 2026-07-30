package socksin

import (
	"net"
	"testing"
	"time"
)

func TestListen(t *testing.T) {
	p := &Plugin{DataDir: t.TempDir()}
	if err := p.Apply(nil, map[string]interface{}{
		"listen_port": 18111,
		"users": []interface{}{
			map[string]interface{}{"username": "t", "password": "p", "enabled": true},
		},
	}); err != nil {
		t.Fatal(err)
	}
	c, err := net.DialTimeout("tcp", "127.0.0.1:18111", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
