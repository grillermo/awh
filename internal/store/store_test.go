package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "awh.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCropDefaultsThenRoundTrips(t *testing.T) {
	s := open(t)
	if got := s.Crop(); got != DefaultCrop {
		t.Fatalf("default %v", got)
	}
	want := [4]float64{0.6035, 0.5494, 0.6426, 0.6233}
	if err := s.SaveCrop(want); err != nil {
		t.Fatal(err)
	}
	if got := s.Crop(); got != want {
		t.Fatalf("got %v", got)
	}
}

func TestReadsWhatPythonWrote(t *testing.T) {
	s := open(t)
	if _, err := s.db.Exec(`INSERT OR REPLACE INTO settings (key, value) VALUES ('display_crop', '[0.6035, 0.5494, 0.6426, 0.6233]')`); err != nil {
		t.Fatal(err)
	}
	if got := s.Crop(); got[3] != 0.6233 {
		t.Fatalf("got %v", got)
	}
}

func TestViewNilThenRoundTrips(t *testing.T) {
	s := open(t)
	if s.View() != nil {
		t.Fatal("want nil view")
	}
	v := map[string]float64{"scale": 2.1, "offsetXFrac": -0.9, "offsetYFrac": -1}
	if err := s.SaveView(v); err != nil {
		t.Fatal(err)
	}
	if got := s.View(); got["scale"] != 2.1 {
		t.Fatalf("got %v", got)
	}
}

func TestSelection(t *testing.T) {
	s := open(t)
	if err := s.UpdateSelection([]string{"text/a", "digit/b"}, nil, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSelection([]string{"text/a"}, []string{"digit/b"}, 2); err != nil {
		t.Fatal(err)
	}
	sel, err := s.Selection()
	if err != nil || len(sel) != 1 || !sel["text/a"] {
		t.Fatalf("got %v %v", sel, err)
	}
}
