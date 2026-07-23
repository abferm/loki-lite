package journal

import (
	"fmt"
	"os"
	"testing"
)

func TestReadRealJournal(t *testing.T) {
	journalPath := os.Getenv("JOURNAL_PATH")
	if journalPath == "" {
		t.Skip("skipping: JOURNAL_PATH not set")
	}

	r, err := Open(journalPath)
	if err != nil {
		t.Fatalf("failed to open journal: %v", err)
	}
	defer r.Close()

	fmt.Printf("Header magic: %s\n", r.Signature())
	fmt.Printf("Header size: %d\n", r.HeaderSize())
	fmt.Printf("N entries: %d\n", r.NEntries())

	count := 0
	for r.Next() {
		entry := r.Entry()
		if entry == nil {
			break
		}
		count++
		if count <= 5 {
			fmt.Printf("Entry %d: %s\n", count, entry)
			fmt.Printf("  Fields: %d\n", len(entry.Fields))
			if msg := entry.Message(); msg != "" {
				fmt.Printf("  Message: %s\n", msg)
			}
			for k, v := range entry.Fields {
				fmt.Printf("    %q = %q\n", k, v)
			}
		}
	}
	fmt.Printf("\nTotal entries read: %d\n", count)
}
