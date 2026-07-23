package journal

import (
	"testing"
	"time"
)

func TestReadHeader(t *testing.T) {
	r, err := Open("testdata/minimal.journal")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	if string(r.header.Signature[:]) != journalMagic {
		t.Errorf("expected signature %q, got %q", journalMagic, string(r.header.Signature[:]))
	}
}

func TestReadEntries(t *testing.T) {
	r, err := Open("testdata/one_entry.journal")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	if !r.Next() {
		t.Fatal("expected at least one entry")
	}

	entry := r.Entry()
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}

	expectedTime := time.Unix(1234567890, 0)
	if !entry.Timestamp.Equal(expectedTime) {
		t.Errorf("expected timestamp %v, got %v", expectedTime, entry.Timestamp)
	}

	if entry.Message() != "Test message" {
		t.Errorf("expected message %q, got %q", "Test message", entry.Message())
	}
}

func TestSeekRealtime(t *testing.T) {
	r, err := Open("testdata/one_entry.journal")
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	target := time.Unix(1234567890, 0)

	r.SeekRealtime(target)
	if !r.Next() {
		t.Fatal("expected entry after SeekRealtime to exact timestamp")
	}
	entry := r.Entry()
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if !entry.Timestamp.Equal(target) {
		t.Errorf("expected timestamp %v, got %v", target, entry.Timestamp)
	}

	r.SeekRealtime(target.Add(time.Microsecond))
	if r.Next() {
		t.Fatal("expected no entries after SeekRealtime to 1usec past")
	}

	r.SeekRealtime(target.Add(-time.Microsecond))
	if !r.Next() {
		t.Fatal("expected entry after SeekRealtime to 1usec before")
	}
	if !r.Entry().Timestamp.Equal(target) {
		t.Errorf("wrong timestamp")
	}
}
