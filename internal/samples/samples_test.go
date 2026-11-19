package samples

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/grillermo/awh/internal/vision"
)

func TestKind(t *testing.T) {
	for text, want := range map[string]string{"": "empty", "  ": "empty", "45": "number", "4 5": "number", "5": "digit", " 6 ": "digit", "4C": "text", "E1": "text"} {
		if got := Kind(text); got != want {
			t.Errorf("Kind(%q) = %q, want %q", text, got, want)
		}
	}
}

func newCollector(t *testing.T, root string, now *float64, rnd float64) *Collector {
	t.Helper()
	c, err := NewCollector(root, MaxBytes, func() float64 { return rnd }, func() float64 { return *now })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTextIsSavedAtMostThreeTimesSpacedApart(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	c := newCollector(t, root, &now, 0.5)
	img := vision.New(4, 4)
	saved := 0
	for i := 0; i < 10; i++ {
		rec, err := c.MaybeSave("4C", img, img, 0.8)
		if err != nil {
			t.Fatal(err)
		}
		if rec != nil {
			saved++
		}
		now += 30 // every other attempt is inside the 60 s gap
	}
	if saved != 3 {
		t.Fatalf("saved %d, want 3", saved)
	}
	// A restart reloads the counters from index.jsonl.
	now += 1000
	if rec, _ := newCollector(t, root, &now, 0.5).MaybeSave("4C", img, img, 0.8); rec != nil {
		t.Fatal("saved a 4th copy after restart")
	}
}

func TestNumbersAreNeverSavedAndEmptiesAreSampled(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	img := vision.New(4, 4)
	if rec, _ := newCollector(t, root, &now, 0.0).MaybeSave("45", img, img, 0.9); rec != nil {
		t.Fatal("saved a number")
	}
	if rec, _ := newCollector(t, root, &now, 0.5).MaybeSave("", img, img, 0); rec != nil {
		t.Fatal("saved an empty above the 2% draw")
	}
	rec, err := newCollector(t, root, &now, 0.01).MaybeSave("", img, img, 0)
	if err != nil || rec == nil || rec.Kind != "empty" {
		t.Fatalf("want an empty sample, got %+v %v", rec, err)
	}
	if _, err := os.Stat(rec.OCR); err != nil {
		t.Fatal(err)
	}
}

func TestSingleDigitsGoInTheirOwnFolder(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	c := newCollector(t, root, &now, 0.5)
	img := vision.New(4, 4)
	rec, _ := c.MaybeSave("5", img, img, 0.8)
	if rec == nil || rec.Kind != "digit" || filepath.Base(filepath.Dir(rec.OCR)) != "digit" {
		t.Fatalf("got %+v", rec)
	}
	if r, _ := c.MaybeSave("5", img, img, 0.8); r != nil {
		t.Fatal("saved a repeat too soon")
	}
}

func TestDiskBudgetStopsSaving(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	img := vision.New(4, 4)
	c, _ := NewCollector(root, 1, func() float64 { return 0 }, func() float64 { return now })
	if rec, _ := c.MaybeSave("E1", img, img, 0.8); rec == nil {
		t.Fatal("first sample should save")
	}
	if rec, _ := c.MaybeSave("ER", img, img, 0.8); rec != nil {
		t.Fatal("saved past the budget")
	}
	c2, _ := NewCollector(root, 1, func() float64 { return 0 }, func() float64 { return now })
	if rec, _ := c2.MaybeSave("XY", img, img, 0.8); rec != nil {
		t.Fatal("restart did not measure the disk")
	}
	if rec, _ := newCollector(t, root, &now, 0).MaybeSave("XY", img, img, 0.8); rec == nil {
		t.Fatal("a larger budget should save")
	}
}

func TestListNewestFirstSkipsMissingFiles(t *testing.T) {
	root, now := t.TempDir(), 1000.0
	c := newCollector(t, root, &now, 0.5)
	img := vision.New(4, 4)
	a, _ := c.MaybeSave("AB", img, img, 0.8)
	now += 100
	b, _ := c.MaybeSave("CD", img, img, 0.8)
	now += 100
	gone, _ := c.MaybeSave("EF", img, img, 0.8)
	os.Remove(gone.OCR)
	list := List(root)
	if len(list) != 2 || list[0].ID != ID(*b) || list[1].ID != ID(*a) {
		t.Fatalf("got %+v", list)
	}
	if list[0].OCRImage != filepath.Join("text", filepath.Base(b.OCR)) {
		t.Fatalf("ocr_image %q", list[0].OCRImage)
	}
}
