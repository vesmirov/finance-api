// finance — the budget planner API.
//
//	finance [-addr 127.0.0.1:8080] [-db <path>]    run the server
//	finance admin create [-login X]                create an admin (asks for a password)
//	finance admin password [-login X]              change a password (revokes sessions)
//
// Database file: the -db flag, else env FINANCE_DB, else ./finance.db.
// It is created on first start; migrations run automatically.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vesmirov/finance-api/internal/rates"
	"github.com/vesmirov/finance-api/internal/server"
	"github.com/vesmirov/finance-api/internal/store"
)

var version = "dev" // -ldflags "-X main.version=..."

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	args := os.Args[1:]
	adminAction := ""
	if len(args) > 1 && args[0] == "admin" {
		adminAction = args[1]
		args = args[2:]
	}

	fs := flag.NewFlagSet("finance", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8080", "listen address")
	dbPath := fs.String("db", "", "SQLite database file (default: env FINANCE_DB or ./finance.db)")
	login := fs.String("login", "", "user login (finance admin ...)")
	adminPassword := fs.String("password", "", "admin password for scripting (finance admin ...); interactive input is safer")
	_ = fs.Parse(args)

	path := *dbPath
	if path == "" {
		path = os.Getenv("FINANCE_DB")
	}
	if path == "" {
		path = "finance.db"
	}

	st, err := store.Open(path)
	if err != nil {
		log.Error("open db", "err", err)
		os.Exit(1)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		log.Error("migrate", "err", err)
		os.Exit(1)
	}

	if adminAction != "" {
		if err := runAdmin(st, adminAction, *login, *adminPassword); err != nil {
			log.Error("admin", "err", err)
			os.Exit(1)
		}
		return
	}

	refreshInterval := 5 * time.Minute
	if v := os.Getenv("FINANCE_RATES_REFRESH"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			log.Error("invalid FINANCE_RATES_REFRESH", "value", v)
			os.Exit(1)
		}
		refreshInterval = d
	}
	stopRefresh := make(chan struct{})
	go rates.Runner(st, &rates.ERAPI{}, refreshInterval, log, stopRefresh)

	srv := server.New(st, log, version)
	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		log.Info("finance api listening", "addr", *addr, "version", version)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			os.Exit(1)
		}
	}()

	// graceful shutdown on SIGTERM/SIGINT — matters for systemd
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	close(stopRefresh)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Error("shutdown", "err", err)
	}
	log.Info("bye")
}
