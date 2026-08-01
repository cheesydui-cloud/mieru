package api

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cheesydui-cloud/mieru/internal/model"
)

func TestMierusShareURLUsesProfileNotDefault(t *testing.T) {
	link := mierusShareURL("kelly", "secret", "211.1.1.1", 10401, "TCP", "kelly-8月6日")
	if link == "" {
		t.Fatal("empty link")
	}
	if !strings.HasPrefix(link, "mierus://") {
		t.Fatalf("scheme: %s", link)
	}
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("profile") != "kelly-8月6日" {
		t.Fatalf("profile=%q want kelly-8月6日 full=%s", q.Get("profile"), link)
	}
	if q.Get("port") != "10401" {
		t.Fatalf("port=%s", q.Get("port"))
	}
	if u.Fragment != "kelly-8月6日" {
		t.Fatalf("fragment=%q", u.Fragment)
	}
}

func TestMierusShareURLEmptyNameDefault(t *testing.T) {
	link := mierusShareURL("u", "p", "1.2.3.4", 10001, "TCP", "")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Query().Get("profile") != "default" {
		t.Fatalf("want default empty name, got %q", u.Query().Get("profile"))
	}
}

func TestClientShareNameWithExpire(t *testing.T) {
	exp := time.Date(2026, 8, 6, 23, 59, 59, 0, time.UTC)
	u := &model.User{Username: "kelly", ExpireAt: &exp}
	got := clientShareName(u)
	if got != "kelly-8月6日" {
		t.Fatalf("got %q", got)
	}
	u2 := &model.User{Username: "bob"}
	if clientShareName(u2) != "bob" {
		t.Fatalf("permanent: %q", clientShareName(u2))
	}
}
