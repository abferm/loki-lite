package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/abferm/loki-lite/engine"
	"github.com/abferm/loki-lite/handler"
	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
	"github.com/abferm/loki-lite/util"
)

func main() {
	journalDir := flag.String("journal-dir", "/var/log/journal", "path to journald directory")
	journalName := flag.String("journal", "", "journal name prefix (empty = all)")
	exclude := flag.String("exclude", "MESSAGE,SYSLOG_TIMESTAMP,_SOURCE_MONOTONIC_TIMESTAMP,_SOURCE_REALTIME_TIMESTAMP", "comma-separated journal fields to exclude from stream labels (high cardinailty)")
	addr := flag.String("addr", ":3100", "listen address")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	})))
	slog.Info("starting loki-lite", "journal_dir", *journalDir, "journal_name", *journalName, "addr", *addr)

	pool := util.NewPool(10, func() *journal.Journal {
		j, err := journal.OpenJournal(*journalDir, *journalName)
		if err != nil {
			panic(fmt.Sprintf("open journal: %v", err))
		}
		return j
	}, func(j *journal.Journal) { j.Close() })
	defer pool.Close()

	schema := model.NewSchema(strings.Split(*exclude, ","))
	eng := engine.New(pool, &schema)
	h := handler.New(eng)

	slog.Info("listening", "addr", *addr)
	if err := http.ListenAndServe(*addr, h.Handler()); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
