package main

import (
	"fmt"
	"os"

	"github.com/abferm/loki-lite/journal"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <journal-dir> <name>\n", os.Args[0])
		os.Exit(1)
	}

	dir := os.Args[1]
	name := os.Args[2]

	j, err := journal.OpenJournal(dir, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenJournal: %v\n", err)
		os.Exit(1)
	}
	defer j.Close()

	fmt.Printf("Journal: %s (%d files)\n", name, j.NFiles())
	fmt.Println()

	for i, r := range j.Files() {
		state := "online"
		switch r.State() {
		case 0:
			state = "offline"
		case 2:
			state = "archived"
		}
		fmt.Printf("File %d: %s\n", i, r.Path())
		fmt.Printf("  Head: %d  Tail: %d  Entries: %d  State: %s\n",
			r.HeadEntrySeqnum(), r.TailEntrySeqnum(), r.NEntries(), state)
	}
}
