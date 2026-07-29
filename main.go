package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/abferm/loki-lite/engine"
	"github.com/abferm/loki-lite/handler"
	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
)

func main() {
	journalDir := flag.String("journal-dir", "/var/log/journal", "path to journald directory")
	journalName := flag.String("journal", "", "journal name prefix (empty = all)")
	exclude := flag.String("exclude", "MESSAGE,SYSLOG_TIMESTAMP,_SOURCE_MONOTONIC_TIMESTAMP,_SOURCE_REALTIME_TIMESTAMP", "comma-separated journal fields to exclude from stream labels (high cardinailty)")
	addr := flag.String("addr", ":3100", "listen address")
	flag.Parse()

	j, err := journal.OpenJournal(*journalDir, *journalName)
	if err != nil {
		log.Fatalf("open journal: %v", err)
	}
	defer j.Close()

	schema := model.NewSchema(strings.Split(*exclude, ","))
	eng := engine.New(j, &schema)
	h := handler.New(eng)

	fmt.Printf("Loki Lite listening on %s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, h.Handler()))
}
