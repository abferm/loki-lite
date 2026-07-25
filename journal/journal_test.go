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

func collectAfterSeek(t *testing.T, j *Journal, target time.Time) []time.Time {
	t.Helper()
	j.SeekRealtime(target)
	var times []time.Time
	if e := j.Entry(); e != nil {
		times = append(times, e.Timestamp)
	}
	for j.Next() {
		e := j.Entry()
		if e == nil {
			t.Fatal("expected non-nil entry")
		}
		times = append(times, e.Timestamp)
	}
	return times
}

func TestSeekRealtime_BeforeFirstEntry(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 1, realtime: 5_000_000},
			{seqnum: 2, realtime: 10_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	times := collectAfterSeek(t, j, time.Unix(1, 0))
	if len(times) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(times))
	}
	if times[0].Unix() != 5 {
		t.Errorf("expected first entry at T=5s, got %v", times[0])
	}
}

func TestSeekRealtime_AfterLastEntry(t *testing.T) {
	// Known issue: SeekRealtime past all entries sets j.entry=nil, but Next()
	// treats nil as "first call" and restarts from head. Expected: 0 entries.
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 1, realtime: 5_000_000},
			{seqnum: 2, realtime: 10_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	j.SeekRealtime(time.Unix(20, 0))
	if j.Entry() == nil {
		t.Fatal("Entry() should latch at last entry after seeking past last")
	}
	if j.Entry().Timestamp.Unix() != 10 {
		t.Errorf("expected latched entry at T=10s, got %v", j.Entry().Timestamp)
	}
	if j.Next() {
		t.Errorf("Next() should return false after seeking past last entry")
	}
}

func TestSeekRealtime_GapBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalOpts{
		state: stateArchived,
		entries: []testEntry{
			{seqnum: 1, realtime: 1_000_000},
			{seqnum: 2, realtime: 2_000_000},
			{seqnum: 3, realtime: 3_000_000},
		},
	})
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 4, realtime: 7_000_000},
			{seqnum: 5, realtime: 8_000_000},
			{seqnum: 6, realtime: 9_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	times := collectAfterSeek(t, j, time.Unix(5, 0))
	if len(times) != 3 {
		t.Fatalf("expected 3 entries (from file B), got %d", len(times))
	}
	if times[0].Unix() != 7 {
		t.Errorf("expected first entry at T=7s, got %v", times[0])
	}
}

func TestSeekRealtime_FileBoundaryTraversal(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalOpts{
		state: stateArchived,
		entries: []testEntry{
			{seqnum: 1, realtime: 1_000_000},
			{seqnum: 2, realtime: 2_000_000},
			{seqnum: 3, realtime: 3_000_000},
			{seqnum: 4, realtime: 4_000_000},
			{seqnum: 5, realtime: 5_000_000},
		},
	})
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 6, realtime: 6_000_000},
			{seqnum: 7, realtime: 7_000_000},
			{seqnum: 8, realtime: 8_000_000},
			{seqnum: 9, realtime: 9_000_000},
			{seqnum: 10, realtime: 10_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	times := collectAfterSeek(t, j, time.Unix(3, 0))
	if len(times) != 8 {
		t.Fatalf("expected 8 entries (T=3..10), got %d", len(times))
	}
	for i, ts := range times {
		expected := int64(i + 3)
		if ts.Unix() != expected {
			t.Errorf("entry %d: expected T=%ds, got %v", i, expected, ts)
		}
	}
}

func TestSeekRealtime_ExactFileBoundary(t *testing.T) {
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalOpts{
		state: stateArchived,
		entries: []testEntry{
			{seqnum: 1, realtime: 1_000_000},
			{seqnum: 2, realtime: 2_000_000},
			{seqnum: 3, realtime: 3_000_000},
			{seqnum: 4, realtime: 4_000_000},
			{seqnum: 5, realtime: 5_000_000},
		},
	})
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 6, realtime: 6_000_000},
			{seqnum: 7, realtime: 7_000_000},
			{seqnum: 8, realtime: 8_000_000},
			{seqnum: 9, realtime: 9_000_000},
			{seqnum: 10, realtime: 10_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	times := collectAfterSeek(t, j, time.Unix(5, 0))
	if len(times) != 6 {
		t.Fatalf("expected 6 entries (T=5..10), got %d", len(times))
	}
	for i, ts := range times {
		expected := int64(i + 5)
		if ts.Unix() != expected {
			t.Errorf("entry %d: expected T=%ds, got %v", i, expected, ts)
		}
	}
}

func TestSeekRealtime_OverlappingTimeRanges(t *testing.T) {
	// File boundary traversal uses seqnum ordering only (realtime is not
	// monotonic). SeekHead on the next file may return entries before the
	// original seek target in time.
	dir := t.TempDir()
	writeTestJournal(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalOpts{
		state: stateArchived,
		entries: []testEntry{
			{seqnum: 1, realtime: 1_000_000},
			{seqnum: 2, realtime: 2_000_000},
			{seqnum: 3, realtime: 3_000_000},
			{seqnum: 4, realtime: 4_000_000},
			{seqnum: 5, realtime: 5_000_000},
			{seqnum: 6, realtime: 6_000_000},
			{seqnum: 7, realtime: 7_000_000},
			{seqnum: 8, realtime: 8_000_000},
		},
	})
	writeTestJournal(t, dir+"/system.journal", testJournalOpts{
		state: stateOnline,
		entries: []testEntry{
			{seqnum: 9, realtime: 5_000_000},
			{seqnum: 10, realtime: 6_000_000},
			{seqnum: 11, realtime: 7_000_000},
			{seqnum: 12, realtime: 8_000_000},
			{seqnum: 13, realtime: 9_000_000},
			{seqnum: 14, realtime: 10_000_000},
			{seqnum: 15, realtime: 11_000_000},
			{seqnum: 16, realtime: 12_000_000},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	j.SeekRealtime(time.Unix(6, 0))
	if j.Entry() == nil {
		t.Fatal("expected non-nil entry after SeekRealtime to T=6s")
	}
	if j.Entry().Timestamp.Unix() != 6 {
		t.Errorf("expected first entry at T=6s, got %v", j.Entry().Timestamp)
	}

	count := 1
	for j.Next() {
		count++
	}
	// T=6,7,8 from A, then SeekHead on B gives T=5,6,7,8,9,10,11,12
	if count != 11 {
		t.Errorf("expected 11 entries, got %d", count)
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
