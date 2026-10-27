package jobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Jerusalem")
	now := time.Date(2026, 10, 27, 3, 0, 0, 0, loc)
	if d := NextRun(now, "03:30"); d != 30*time.Minute {
		t.Fatal(d)
	}
	if d := NextRun(now.Add(31*time.Minute), "03:30"); d != 23*time.Hour+59*time.Minute {
		t.Fatal(d)
	}
	if d := NextRun(now.Add(30*time.Minute), "03:30"); d != 24*time.Hour {
		t.Fatalf("exactly at the time schedules tomorrow: %v", d)
	}
	if NextRun(now, "bogus") != 24*time.Hour {
		t.Fatal("bad time")
	}
}

func TestPrune(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z"} {
		os.WriteFile(filepath.Join(dir, "recipes-"+n+".db"), nil, 0o644)
		os.WriteFile(filepath.Join(dir, "recipes-"+n+".media.txt"), nil, 0o644)
	}
	os.WriteFile(filepath.Join(dir, "unrelated.txt"), nil, 0o644)
	Prune(dir, 2)
	m, _ := filepath.Glob(filepath.Join(dir, "recipes-*"))
	if len(m) != 4 {
		t.Fatalf("%v", m)
	}
	if _, err := os.Stat(filepath.Join(dir, "recipes-20260101T000000Z.media.txt")); err == nil {
		t.Fatal("oldest media list should go with its snapshot")
	}
	if _, err := os.Stat(filepath.Join(dir, "unrelated.txt")); err != nil {
		t.Fatal("unrelated file removed")
	}
}
