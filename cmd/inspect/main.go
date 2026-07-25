package main

import (
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/abferm/loki-lite/journal"
)

const inspectFieldValuesLimit = 100

func main() {
	statsFlag := flag.Bool("stats", false, "print journal file stats")
	fieldsFlag := flag.Bool("fields", false, "print field names")
	fieldValuesFlag := flag.String("field-values", "", "print distinct values for the named field")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <journal-dir> <name>\n\nFlags:\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()

	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(1)
	}

	dir := flag.Arg(0)
	name := flag.Arg(1)

	// Default to stats if no flag is set.
	if !*statsFlag && !*fieldsFlag && *fieldValuesFlag == "" {
		*statsFlag = true
	}

	j, err := journal.OpenJournal(dir, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "OpenJournal: %v\n", err)
		os.Exit(1)
	}
	defer j.Close()

	if *statsFlag {
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

	if *fieldsFlag {
		fields, err := j.Fields()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fields: %v\n", err)
			os.Exit(1)
		}
		sort.Strings(fields)
		for _, f := range fields {
			fmt.Println(f)
		}
	}

	if *fieldValuesFlag != "" {
		vals, truncated, err := j.FieldValues(*fieldValuesFlag, inspectFieldValuesLimit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FieldValues: %v\n", err)
			os.Exit(1)
		}
		sort.Strings(vals)
		for _, v := range vals {
			fmt.Println(v)
		}
		if truncated {
			fmt.Fprintf(os.Stderr, "(truncated at %d values)\n", inspectFieldValuesLimit)
		}
	}
}
