package store_test

import (
	"path/filepath"
	"testing"

	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
)

func TestClientFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	a := &model.ClientFile{
		Title:       "客户端",
		Filename:    "client.zip",
		StoredName:  "uuid-a",
		Size:        1024,
		ContentType: "application/zip",
		Enabled:     true,
	}
	if err := s.CreateClientFile(a); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 {
		t.Fatal("id")
	}

	b := &model.ClientFile{
		Title:      "隐藏",
		Filename:   "hidden.bin",
		StoredName: "uuid-b",
		Size:       8,
		Enabled:    false,
	}
	if err := s.CreateClientFile(b); err != nil {
		t.Fatal(err)
	}

	pub, err := s.ListPublicClientFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(pub) != 1 || pub[0].ID != a.ID {
		t.Fatalf("public want 1 got %#v", pub)
	}

	all, err := s.ListClientFiles()
	if err != nil || len(all) != 2 {
		t.Fatalf("all %v %d", err, len(all))
	}

	got, err := s.GetClientFile(a.ID)
	if err != nil || got == nil || got.StoredName != "uuid-a" {
		t.Fatalf("get %#v %v", got, err)
	}

	got.Title = "客户端 v2"
	got.Enabled = false
	if err := s.UpdateClientFile(got); err != nil {
		t.Fatal(err)
	}
	pub, _ = s.ListPublicClientFiles()
	if len(pub) != 0 {
		t.Fatalf("public after disable %#v", pub)
	}

	stored, err := s.DeleteClientFile(a.ID)
	if err != nil || stored != "uuid-a" {
		t.Fatalf("delete %q %v", stored, err)
	}
	all, _ = s.ListClientFiles()
	if len(all) != 1 {
		t.Fatalf("after delete %d", len(all))
	}
}
