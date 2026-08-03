package agentcore

import "testing"

func TestRewriteAgentPanelURLEnv(t *testing.T) {
	in := "AGENT_PANEL_URL=http://old:8080\nAGENT_NODE_ID=n1\nAGENT_TOKEN=tok\n"
	out, changed, err := rewriteAgentPanelURLEnv(in, "https://new.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected change")
	}
	if !containsLine(out, "AGENT_PANEL_URL=https://new.example.com") {
		t.Fatalf("out=%q", out)
	}
	if !containsLine(out, "AGENT_NODE_ID=n1") {
		t.Fatalf("lost node id: %q", out)
	}

	// idempotent
	out2, changed2, err := rewriteAgentPanelURLEnv(out, "https://new.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Fatalf("expected no change, got %q", out2)
	}

	// missing key gets inserted
	in2 := "AGENT_NODE_ID=n1\nAGENT_TOKEN=tok\n"
	out3, changed3, err := rewriteAgentPanelURLEnv(in2, "http://p:8080")
	if err != nil || !changed3 {
		t.Fatalf("err=%v changed=%v out=%q", err, changed3, out3)
	}
	if !containsLine(out3, "AGENT_PANEL_URL=http://p:8080") {
		t.Fatalf("out3=%q", out3)
	}

	// legacy PANEL_URL also updated
	in3 := "AGENT_PANEL_URL=http://a\nPANEL_URL=http://a\n"
	out4, changed4, err := rewriteAgentPanelURLEnv(in3, "http://b")
	if err != nil || !changed4 {
		t.Fatalf("err=%v changed=%v", err, changed4)
	}
	if !containsLine(out4, "PANEL_URL=http://b") || !containsLine(out4, "AGENT_PANEL_URL=http://b") {
		t.Fatalf("out4=%q", out4)
	}
}

func TestNormalizeAgentPanelURL(t *testing.T) {
	if got := normalizeAgentPanelURL(" example.com/ "); got != "http://example.com" {
		t.Fatalf("got %q", got)
	}
	if got := normalizeAgentPanelURL("https://x/"); got != "https://x" {
		t.Fatalf("got %q", got)
	}
}

func containsLine(s, line string) bool {
	for _, l := range splitLines(s) {
		if l == line {
			return true
		}
	}
	return false
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start <= len(s) {
		out = append(out, s[start:])
	}
	return out
}
