package store_test

import (
	"github.com/cheesydui-cloud/mieru/internal/model"
	"github.com/cheesydui-cloud/mieru/internal/store"
	"path/filepath"
	"testing"
)

func TestAnnouncements(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	a := &model.Announcement{Title: "维护", Body: "今晚 2 点", Enabled: true, Popup: true}
	if err := s.CreateAnnouncement(a); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 {
		t.Fatal("id")
	}
	b := &model.Announcement{Title: "活动", Body: "送流量", Enabled: true, Popup: true}
	if err := s.CreateAnnouncement(b); err != nil {
		t.Fatal(err)
	}
	// only one popup
	pop, err := s.PopupAnnouncement()
	if err != nil {
		t.Fatal(err)
	}
	if pop == nil || pop.ID != b.ID {
		t.Fatalf("popup want %d got %#v", b.ID, pop)
	}
	list, err := s.ListPublicAnnouncements()
	if err != nil || len(list) != 2 {
		t.Fatalf("list %v %d", err, len(list))
	}
	if err := s.SetAnnouncementPopup(a.ID, true); err != nil {
		t.Fatal(err)
	}
	pop, _ = s.PopupAnnouncement()
	if pop.ID != a.ID {
		t.Fatalf("popup switch %d", pop.ID)
	}
	if err := s.DeleteAnnouncement(a.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListAnnouncements()
	if len(list) != 1 {
		t.Fatalf("after del %d", len(list))
	}
}
