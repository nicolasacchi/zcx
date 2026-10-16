package store

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nicolasacchi/zcx/internal/client"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := OpenAt(filepath.Join(t.TempDir(), "zcx"), "21356")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func conv(id int64, created string) *client.Conversation {
	ts, _ := time.Parse(time.RFC3339, created)
	return &client.Conversation{ID: id, CreatedAt: ts, UpdatedAt: ts.Add(time.Microsecond * 123456), Messages: []client.Message{{ID: id * 10, CreatedAt: ts, Role: "user", Message: "synthetic"}}}
}

func TestPermissions(t *testing.T) {
	s := open(t)
	if err := s.SaveConversation(conv(1, "2026-10-01T10:00:00Z")); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveState(State{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BackupPrompt("p"); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(filepath.Dir(s.Dir), func(p string, d os.DirEntry, err error) error {
		info, _ := d.Info()
		perm := info.Mode().Perm()
		if d.IsDir() && perm != 0o700 {
			t.Errorf("dir %s %o", p, perm)
		}
		if !d.IsDir() && perm != 0o600 {
			t.Errorf("file %s %o", p, perm)
		}
		return nil
	})
}

func TestConversationRoundTripAndFilter(t *testing.T) {
	s := open(t)
	for id, created := range map[int64]string{3: "2026-10-03T10:00:00Z", 1: "2026-10-01T10:00:00Z", 2: "2026-10-02T10:00:00Z"} {
		if err := s.SaveConversation(conv(id, created)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LoadConversation(2)
	if err != nil || len(got.Messages) != 1 || got.UpdatedAt.Nanosecond() != 123456000 {
		t.Fatalf("%v %+v", err, got)
	}
	all, _ := s.LoadAll(nil, nil)
	if len(all) != 3 || all[0].ID != 1 || all[2].ID != 3 {
		t.Fatalf("order: %+v", all)
	}
	from := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC) // exclusive
	sub, _ := s.LoadAll(&from, &to)
	if len(sub) != 1 || sub[0].ID != 2 {
		t.Fatalf("filter: %+v", sub)
	}
	if _, err := s.LoadConversation(99); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.SaveConversation(conv(2, "2026-10-02T10:00:00Z")); err != nil { // overwrite
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(s.convDir())
	if len(ents) != 3 {
		t.Errorf("temp files leaked: %d entries", len(ents))
	}
}

func TestState(t *testing.T) {
	s := open(t)
	st, err := s.State()
	if err != nil || st.UpdatedAtHighWater != nil {
		t.Fatalf("%v %+v", err, st)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.SaveState(State{UpdatedAtHighWater: &now, LastSync: &now}); err != nil {
		t.Fatal(err)
	}
	st, _ = s.State()
	if !st.UpdatedAtHighWater.Equal(now) || st.LastMessageAtHighWater != nil {
		t.Errorf("%+v", st)
	}
}

func TestPromptBackups(t *testing.T) {
	s := open(t)
	p1, err := s.BackupPrompt("first prompt")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	p2, _ := s.BackupPrompt("second prompt")
	list, err := s.ListPromptBackups()
	if err != nil || len(list) != 2 || list[0].Path != p2 || list[1].Path != p1 {
		t.Fatalf("%v %+v", err, list)
	}
	if list[1].SHA256 != SHA256("first prompt") || list[1].Size != 12 {
		t.Errorf("%+v", list[1])
	}
	for _, ref := range []string{list[1].Name, p1} {
		txt, err := s.ReadPromptBackup(ref)
		if err != nil || txt != "first prompt" {
			t.Errorf("read %s: %q %v", ref, txt, err)
		}
	}
	if _, err := s.ReadPromptBackup("/etc/passwd"); err == nil {
		t.Error("path outside prompts/ must be rejected")
	}
	if _, err := s.ReadPromptBackup("nope.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("%v", err)
	}
}

func TestInvalidChatID(t *testing.T) {
	for _, id := range []string{"", "..", "a/b"} {
		if _, err := OpenAt(t.TempDir(), id); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
}

func TestConcurrentSaveLoad(t *testing.T) {
	s, err := OpenAt(t.TempDir(), "7")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SaveConversation(&client.Conversation{ID: i}); err != nil {
				t.Error(err)
				return
			}
			if c, err := s.LoadConversation(i); err != nil || c.ID != i {
				t.Errorf("load %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	ents, _ := os.ReadDir(s.convDir())
	if len(ents) != 32 {
		t.Fatalf("%d files (leftover temp files?)", len(ents))
	}
	for _, e := range ents {
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", e.Name(), fi.Mode().Perm())
		}
	}
	if fi, _ := os.Stat(s.convDir()); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
}
