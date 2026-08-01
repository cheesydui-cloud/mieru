package agentcore

import (
	"testing"

	"github.com/cheesydui-cloud/mieru/internal/config"
	"github.com/cheesydui-cloud/mieru/internal/model"
)

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
	if got := parseHumanSize("-"); got != 0 {
		t.Fatalf("- => %d", got)
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

func TestParseMitaUsersTableNeverActive(t *testing.T) {
	out := `User  LastActive  1DayDownload  1DayUpload  30DaysDownload  30DaysUpload
bob   never       0B            0B          0B              0B
carol -           10KiB         2KiB        10KiB           2KiB
`
	m := parseMitaUsersTable(out)
	if len(m) != 2 {
		t.Fatalf("users=%d %#v", len(m), m)
	}
	if m["bob"].down != 0 || m["bob"].up != 0 {
		t.Fatalf("bob %#v", m["bob"])
	}
	if m["carol"].down < 10*1024-1 || m["carol"].up < 2*1024-1 {
		t.Fatalf("carol %#v", m["carol"])
	}
}

func TestParseMitaUsersTableModernHeaders(t *testing.T) {
	out := `User   LastActive             1DayDown  1DayUp   7DaysDown  7DaysUp  30DaysDown  30DaysUp
alice  2025-04-23T01:02:03Z   938.1MiB  12.9MiB  4.0GiB     31.8MiB  4.0GiB      31.8MiB
bob    -                      -         -        -          -        -           -
carol  2025-04-23T01:02:03Z   0B        0B       0B         0B       0B          0B
`
	m := parseMitaUsersTable(out)
	if len(m) != 3 {
		t.Fatalf("users=%d %#v", len(m), m)
	}
	if m["alice"].down < 900*1024*1024 {
		t.Fatalf("alice down=%d", m["alice"].down)
	}
	if m["alice"].up < 12*1024*1024 {
		t.Fatalf("alice up=%d", m["alice"].up)
	}
	if m["bob"].down != 0 || m["bob"].up != 0 {
		t.Fatalf("bob should be zero for '-', got %#v", m["bob"])
	}
}

func TestParseMitaMetricsUsersJSON(t *testing.T) {
	out := `{
    "connections": {
        "ActiveOpens": 10,
        "CurrEstablished": 1
    },
    "traffic": {
        "DownloadBytes": 999,
        "UploadBytes": 111
    },
    "users": {
        "alice": {
            "DownloadBytes": 1048576,
            "UploadBytes": 2048
        },
        "bob": {
            "DownloadBytes": 0,
            "UploadBytes": 0
        }
    }
}`
	m := parseMitaMetricsUsersJSON(out)
	if len(m) != 2 {
		t.Fatalf("users=%d %#v", len(m), m)
	}
	if m["alice"].down != 1048576 {
		t.Fatalf("alice down=%d", m["alice"].down)
	}
	if m["alice"].up != 2048 {
		t.Fatalf("alice up=%d", m["alice"].up)
	}
}

func TestParseMitaMetricsUsersJSONWithNoise(t *testing.T) {
	out := "some log line\n{\n  \"users\": {\"u1\": {\"DownloadBytes\": 50, \"UploadBytes\": 7}}\n}\n"
	m := parseMitaMetricsUsersJSON(out)
	if m["u1"].down != 50 || m["u1"].up != 7 {
		t.Fatalf("%#v", m)
	}
}

func TestShouldMeterTrafficFromDesired(t *testing.T) {
	a := New(config.AgentConfig{Role: model.RoleEntry, DataDir: t.TempDir()})
	if a.shouldMeterTraffic() {
		t.Fatal("entry without desired should not meter")
	}
	// panel says exit in desired plugins
	a.updateMeteringFromDesired(&model.AgentDesiredConfig{
		Role: model.RoleEntry, // env role wrong
		Plugins: []map[string]interface{}{
			{"type": "mita_server", "config": map[string]interface{}{}},
		},
	})
	if !a.shouldMeterTraffic() {
		t.Fatal("mita_server plugin must enable metering even if env role is entry")
	}
	// front only
	a2 := New(config.AgentConfig{Role: model.RoleRelay, DataDir: t.TempDir()})
	a2.updateMeteringFromDesired(&model.AgentDesiredConfig{
		Role: model.RoleRelay,
		Plugins: []map[string]interface{}{
			{"type": "tcp_forward"},
		},
	})
	if a2.shouldMeterTraffic() {
		t.Fatal("front tcp_forward must not enable metering")
	}
	// role exit without plugins still meters
	a3 := New(config.AgentConfig{Role: "wrong", DataDir: t.TempDir()})
	a3.updateMeteringFromDesired(&model.AgentDesiredConfig{Role: model.RoleExit})
	if !a3.shouldMeterTraffic() {
		t.Fatal("desired role=exit must enable metering")
	}
}
