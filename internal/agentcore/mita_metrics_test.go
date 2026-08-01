package agentcore

import "testing"

func TestParseHumanSize(t *testing.T) {
	if got := parseHumanSize("100B"); got != 100 {
		t.Fatalf("100B => %d", got)
	}
	if got := parseHumanSize("1KB"); got != 1024 {
		t.Fatalf("1KB => %d", got)
	}
	if got := parseHumanSize("12.9MiB"); got < 13_000_000 || got > 14_000_000 {
		t.Fatalf("12.9MiB => %d", got)
	}
	if got := parseHumanSize("4.0GiB"); got < 4*1024*1024*1024-1000 {
		t.Fatalf("4GiB => %d", got)
	}
	if got := parseHumanSize(""); got != 0 {
		t.Fatalf("empty => %d", got)
	}
}

func TestParseMitaUsersTable(t *testing.T) {
	out := `User  LastActive            1DayDownload  1DayUpload  30DaysDownload  30DaysUpload
abcd  2025-04-23T01:02:03Z  938.1MiB      12.9MiB     4.0GiB          31.8MiB
aaa   2025-04-23T01:02:03Z  1.0MiB        0.5MiB      1.0MiB          0.5MiB
`
	m := parseMitaUsersTable(out)
	if len(m) != 2 {
		t.Fatalf("users=%d %#v", len(m), m)
	}
	if m["abcd"].down < 900*1024*1024 {
		t.Fatalf("abcd down=%d", m["abcd"].down)
	}
	if m["aaa"].up < 400*1024 {
		t.Fatalf("aaa up=%d", m["aaa"].up)
	}
}
