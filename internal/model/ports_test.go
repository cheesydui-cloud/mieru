package model

import "testing"

func TestEffectivePortRangeFrontExpandsSingle(t *testing.T) {
	n := &Node{Role: RoleRelay, PortMin: 10401, PortMax: 10401}
	min, max := n.EffectivePortRange()
	if min != 10401 || max != 10401+DefaultFrontPortPoolSpan {
		t.Fatalf("got %d-%d want 10401-%d", min, max, 10401+DefaultFrontPortPoolSpan)
	}
}

func TestEffectivePortRangeFrontKeepsExplicit(t *testing.T) {
	n := &Node{Role: RoleRelay, PortMin: 10401, PortMax: 10450}
	min, max := n.EffectivePortRange()
	if min != 10401 || max != 10450 {
		t.Fatalf("got %d-%d", min, max)
	}
}

func TestEffectivePortRangeExitStaysSingle(t *testing.T) {
	n := &Node{Role: RoleExit, PortMin: 10001, PortMax: 10001}
	min, max := n.EffectivePortRange()
	if min != 10001 || max != 10001 {
		t.Fatalf("exit should stay single, got %d-%d", min, max)
	}
}
