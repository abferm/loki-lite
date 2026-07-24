package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

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
	j.SeekHead()

	fmt.Fprintf(os.Stderr, "Following %s (%d files)\n", name, j.NFiles())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	j.Follow(ctx, func(e *journal.Entry) bool {
		msg := e.Get("MESSAGE")
		if msg == "" {
			msg = "no MESSAGE"
		}
		msg = strings.TrimSpace(msg)
		fmt.Printf("%v %s %s\n", e.Timestamp.Format("15:04:05.000"), e.Unit(), msg)
		return true
	})
}
