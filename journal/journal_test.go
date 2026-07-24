package journal

import (
	"testing"
	"time"
)

func TestOpenJournal(t *testing.T) {
	j, err := OpenJournal("testdata/multi", "system")
	if err != nil {
		t.Fatalf("OpenJournal failed: %v", err)
	}
	defer j.Close()

	if j.NFiles() != 2 {
		t.Errorf("expected 2 files, got %d", j.NFiles())
	}
}

func TestJournalReadEntries(t *testing.T) {
	j, err := OpenJournal("testdata/multi", "system")
	if err != nil {
		t.Fatalf("OpenJournal failed: %v", err)
	}
	defer j.Close()

	count := 0
	for j.Next() {
		entry := j.Entry()
		if entry == nil {
			t.Fatal("expected non-nil entry")
		}
		count++
	}

	if count != 4 {
		t.Errorf("expected 4 entries, got %d", count)
	}
}

func TestJournalTimestampOrder(t *testing.T) {
	j, err := OpenJournal("testdata/multi", "system")
	if err != nil {
		t.Fatalf("OpenJournal failed: %v", err)
	}
	defer j.Close()

	var prevTime time.Time
	for j.Next() {
		entry := j.Entry()
		if entry == nil {
			t.Fatal("expected non-nil entry")
		}

		if !prevTime.IsZero() && entry.Timestamp.Before(prevTime) {
			t.Errorf("entries not in chronological order: %v before %v", entry.Timestamp, prevTime)
		}
		prevTime = entry.Timestamp
	}
}

func TestJournalSeekRealtime(t *testing.T) {
	j, err := OpenJournal("testdata/multi", "system")
	if err != nil {
		t.Fatalf("OpenJournal failed: %v", err)
	}
	defer j.Close()

	target := time.Unix(0, 3000000*1000) // 3 seconds
	j.SeekRealtime(target)

	count := 0
	if entry := j.Entry(); entry != nil {
		if entry.Timestamp.Before(target) {
			t.Errorf("entry %v is before target %v", entry.Timestamp, target)
		}
		count++
	}
	for j.Next() {
		entry := j.Entry()
		if entry == nil {
			t.Fatal("expected non-nil entry")
		}

		if entry.Timestamp.Before(target) {
			t.Errorf("entry %v is before target %v", entry.Timestamp, target)
		}
		count++
	}

	if count != 2 {
		t.Errorf("expected 2 entries after SeekRealtime, got %d", count)
	}
}

func TestJournalSeekHead(t *testing.T) {
	j, err := OpenJournal("testdata/multi", "system")
	if err != nil {
		t.Fatalf("OpenJournal failed: %v", err)
	}
	defer j.Close()

	// Read some entries
	j.Next()
	j.Next()

	// Seek back to head
	j.SeekHead()

	count := 0
	for j.Next() {
		entry := j.Entry()
		if entry == nil {
			t.Fatal("expected non-nil entry")
		}
		count++
	}

	if count != 4 {
		t.Errorf("expected 4 entries after SeekHead, got %d", count)
	}
}
