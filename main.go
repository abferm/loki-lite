package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/abferm/loki-lite/config"
	"github.com/abferm/loki-lite/engine"
	"github.com/abferm/loki-lite/handler"
	"github.com/abferm/loki-lite/journal"
	"github.com/abferm/loki-lite/model"
	"github.com/abferm/loki-lite/util"
)

func main() {
	cfgPath := flag.String("config", "", "path to TOML config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(1)
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelInfo,
	})))
	slog.Info("starting loki-lite", "journal_dir", cfg.Journal.Dir, "journal_name", cfg.Journal.Name, "addr", cfg.Server.Addr)

	pool := util.NewPool(cfg.Server.PoolMax, func() *journal.Journal {
		j, err := journal.OpenJournal(cfg.Journal.Dir, cfg.Journal.Name)
		if err != nil {
			panic(fmt.Sprintf("open journal: %v", err))
		}
		return j
	}, func(j *journal.Journal) { j.Close() })
	defer pool.Close()

	schema := model.NewSchema(cfg.Schema.Exclude)
	eng := engine.New(pool, &schema)
	h := handler.New(eng)

	slog.Info("listening", "addr", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, h.Handler()); err != nil {
		slog.Error("server error", "error", err)
		os.Exit(1)
	}
}
