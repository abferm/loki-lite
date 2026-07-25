package journal

import (
	"sort"
	"testing"
)

func TestFileFields(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "hello", "SYSLOG_IDENTIFIER": "svc1"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "world", "_SYSTEMD_UNIT": "foo.service"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	fields, err := f.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}

	sort.Strings(fields)
	expected := []string{"MESSAGE", "SYSLOG_IDENTIFIER", "_SYSTEMD_UNIT"}
	if len(fields) != len(expected) {
		t.Fatalf("expected %d fields, got %d: %v", len(expected), len(fields), fields)
	}
	for i, name := range fields {
		if name != expected[i] {
			t.Errorf("field[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestFileFieldValues(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "hello", "SYSLOG_IDENTIFIER": "svc1"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "world", "SYSLOG_IDENTIFIER": "svc1"}},
			{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "again", "SYSLOG_IDENTIFIER": "svc2"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	vals, truncated, err := f.FieldValues("MESSAGE", 100)
	if err != nil {
		t.Fatalf("FieldValues(MESSAGE): %v", err)
	}
	if truncated {
		t.Fatal("expected truncated=false")
	}
	sort.Strings(vals)
	expected := []string{"again", "hello", "world"}
	if len(vals) != len(expected) {
		t.Fatalf("expected %d values, got %d: %v", len(expected), len(vals), vals)
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("value[%d] = %q, want %q", i, v, expected[i])
		}
	}

	// Nonexistent field should return nil.
	vals, truncated, err = f.FieldValues("NONEXISTENT", 100)
	if err != nil {
		t.Fatalf("FieldValues(NONEXISTENT): %v", err)
	}
	if truncated {
		t.Fatal("expected truncated=false for nonexistent field")
	}
	if len(vals) != 0 {
		t.Errorf("expected nil for nonexistent field, got %v", vals)
	}
}

func TestFileFieldsCaching(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "hello"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	fields1, err := f.Fields()
	if err != nil {
		t.Fatalf("Fields (1st call): %v", err)
	}
	fields2, err := f.Fields()
	if err != nil {
		t.Fatalf("Fields (2nd call): %v", err)
	}

	// Archived file should cache and return the same slice.
	if len(fields1) != 1 || fields1[0] != "MESSAGE" {
		t.Fatalf("unexpected fields: %v", fields1)
	}
	if len(fields2) != 1 || fields2[0] != "MESSAGE" {
		t.Fatalf("unexpected cached fields: %v", fields2)
	}
}

func TestFileFieldValuesLimit(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "a"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "b"}},
			{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "c"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	vals, truncated, err := f.FieldValues("MESSAGE", 2)
	if err != nil {
		t.Fatalf("FieldValues: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 partial values, got %d: %v", len(vals), vals)
	}
}

func TestFileFieldValuesLimitExact(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "a"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "b"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	vals, truncated, err := f.FieldValues("MESSAGE", 2)
	if err != nil {
		t.Fatalf("FieldValues: %v", err)
	}
	if truncated {
		t.Fatal("expected truncated=false")
	}
	if len(vals) != 2 {
		t.Fatalf("expected 2 values, got %d: %v", len(vals), vals)
	}
}

func TestFileFieldValuesTruncatedNotCached(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "a"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "b"}},
			{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "c"}},
		},
	})

	f, err := Open(dir + "/system.journal")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()

	// First call with limit=2 — should truncate and NOT cache.
	vals1, truncated1, err := f.FieldValues("MESSAGE", 2)
	if err != nil {
		t.Fatalf("FieldValues (limit 2): %v", err)
	}
	if !truncated1 {
		t.Fatal("expected truncated=true")
	}
	if len(vals1) != 2 {
		t.Fatalf("expected 2 partial values, got %d: %v", len(vals1), vals1)
	}

	// Second call with limit=100 — should re-scan and return full results.
	vals2, truncated2, err := f.FieldValues("MESSAGE", 100)
	if err != nil {
		t.Fatalf("FieldValues (limit 100): %v", err)
	}
	if truncated2 {
		t.Fatal("expected truncated=false on retry")
	}
	if len(vals2) != 3 {
		t.Fatalf("expected 3 values on retry, got %d: %v", len(vals2), vals2)
	}

	// Third call with limit=100 — should return cached result.
	vals3, truncated3, err := f.FieldValues("MESSAGE", 100)
	if err != nil {
		t.Fatalf("FieldValues (cached): %v", err)
	}
	if truncated3 {
		t.Fatal("expected truncated=false on cached")
	}
	if len(vals3) != 3 {
		t.Fatalf("expected 3 cached values, got %d: %v", len(vals3), vals3)
	}
}

func TestJournalFields(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "hello", "SYSLOG_IDENTIFIER": "svc1"}},
		},
	})
	writeTestJournalWithFields(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "world", "_HOSTNAME": "host1"}},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	fields, err := j.Fields()
	if err != nil {
		t.Fatalf("Fields: %v", err)
	}
	sort.Strings(fields)
	expected := []string{"MESSAGE", "SYSLOG_IDENTIFIER", "_HOSTNAME"}
	if len(fields) != len(expected) {
		t.Fatalf("expected %d fields, got %d: %v", len(expected), len(fields), fields)
	}
	for i, name := range fields {
		if name != expected[i] {
			t.Errorf("field[%d] = %q, want %q", i, name, expected[i])
		}
	}
}

func TestJournalFieldValues(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"SYSLOG_IDENTIFIER": "svc1"}},
		},
	})
	writeTestJournalWithFields(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"SYSLOG_IDENTIFIER": "svc2"}},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	vals, truncated, err := j.FieldValues("SYSLOG_IDENTIFIER", 100)
	if err != nil {
		t.Fatalf("FieldValues: %v", err)
	}
	if truncated {
		t.Fatal("expected truncated=false")
	}
	sort.Strings(vals)
	expected := []string{"svc1", "svc2"}
	if len(vals) != len(expected) {
		t.Fatalf("expected %d values, got %d: %v", len(expected), len(vals), vals)
	}
	for i, v := range vals {
		if v != expected[i] {
			t.Errorf("value[%d] = %q, want %q", i, v, expected[i])
		}
	}
}

func TestJournalFieldValuesLimit(t *testing.T) {
	dir := t.TempDir()
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "a"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "b"}},
			{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "c"}},
		},
	})
	writeTestJournalWithFields(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 4, realtime: 4000000, fields: map[string]string{"MESSAGE": "d"}},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	// 4 unique MESSAGE values across 2 files, limit=3 → truncated.
	vals, truncated, err := j.FieldValues("MESSAGE", 3)
	if err != nil {
		t.Fatalf("FieldValues: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 partial values, got %d: %v", len(vals), vals)
	}
}

func TestJournalFieldValuesLimitExceedsAfterMerge(t *testing.T) {
	dir := t.TempDir()
	// File A: 2 unique values, File B: 2 unique values, all distinct → 4 total.
	writeTestJournalWithFields(t, dir+"/system.journal", testJournalWithFieldsOpts{
		state: stateOnline,
		entries: []testFieldEntry{
			{seqnum: 1, realtime: 1000000, fields: map[string]string{"MESSAGE": "a"}},
			{seqnum: 2, realtime: 2000000, fields: map[string]string{"MESSAGE": "b"}},
		},
	})
	writeTestJournalWithFields(t, dir+"/system@0000000000000001-0000000000000001.journal", testJournalWithFieldsOpts{
		state: stateArchived,
		entries: []testFieldEntry{
			{seqnum: 3, realtime: 3000000, fields: map[string]string{"MESSAGE": "c"}},
			{seqnum: 4, realtime: 4000000, fields: map[string]string{"MESSAGE": "d"}},
		},
	})

	j, err := OpenJournal(dir, "system")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	defer j.Close()

	// Each file has 2 values (under limit=3), but merged total is 4 (over limit).
	vals, truncated, err := j.FieldValues("MESSAGE", 3)
	if err != nil {
		t.Fatalf("FieldValues: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncated=true when merged count exceeds limit")
	}
	if len(vals) != 3 {
		t.Fatalf("expected 3 partial values, got %d: %v", len(vals), vals)
	}
}
