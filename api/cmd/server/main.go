// Command server runs the db-compare HTTP API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dbcompare/internal/api"
	"dbcompare/internal/auth"
	"dbcompare/internal/config"
	"dbcompare/internal/runner"
	"dbcompare/internal/secret"
	"dbcompare/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.EnvFile != "" {
		slog.Info("loaded env file", "path", cfg.EnvFile)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	box, err := secret.NewBox(cfg.SecretKey)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.AppDatabaseURL, box)
	if err != nil {
		return err
	}
	defer st.Close()

	if err := ensureAdmin(ctx, st, cfg); err != nil {
		return err
	}

	// Work marked running by a previous process has no live request behind
	// it. This assumes a single server instance.
	if n, err := st.MarkStaleRuns(ctx, 0); err != nil {
		return err
	} else if n > 0 {
		slog.Info("marked interrupted runs", "count", n)
	}
	if err := st.MarkRowDiffsInterrupted(ctx); err != nil {
		return err
	}

	pools := runner.NewPools(st, cfg.MaxConnsPerDB)
	defer pools.Close()
	runs := runner.NewManager(st, pools, runner.Config{
		MaxParallelism:    min(cfg.MaxParallelTables, cfg.MaxConnsPerDB-1),
		QueryTimeout:      cfg.QueryTimeout,
		HeartbeatInterval: cfg.HeartbeatInterval,
	})

	go maintain(ctx, st, cfg)

	// Request contexts derive from baseCtx, so cancelling it on shutdown ends
	// open event streams and the runs they drive.
	baseCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           api.New(cfg, st, runs, pools).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		BaseContext:       func(net.Listener) context.Context { return baseCtx },
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.ListenAddr, "session_ttl", cfg.SessionTTL.String())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	runs.CancelAll()
	cancelRequests()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// maintain deletes runs past the retention period and flags runs whose
// heartbeat stopped.
func maintain(ctx context.Context, st *store.Store, cfg *config.Config) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if cfg.RetentionDays > 0 {
			cutoff := time.Now().AddDate(0, 0, -cfg.RetentionDays)
			if n, err := st.DeleteRunsBefore(ctx, cutoff); err != nil {
				slog.Error("retention cleanup", "err", err)
			} else if n > 0 {
				slog.Info("deleted expired runs", "count", n)
			}
		}
		if _, err := st.MarkStaleRuns(ctx, 3*cfg.HeartbeatInterval); err != nil {
			slog.Error("mark stale runs", "err", err)
		}
		if _, err := st.DeleteExpiredSessions(ctx); err != nil {
			slog.Error("delete expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// ensureAdmin creates the first admin when there are no users, so a fresh
// installation can be signed in to. Without ADMIN_PASSWORD a random password
// is generated and written to the log once.
func ensureAdmin(ctx context.Context, st *store.Store, cfg *config.Config) error {
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return err
	}
	username := strings.ToLower(strings.TrimSpace(cfg.AdminUsername))
	if !auth.ValidUsername(username) {
		return fmt.Errorf("ADMIN_USERNAME %q is not a valid username", cfg.AdminUsername)
	}
	password, generated := cfg.AdminPassword, false
	if password == "" {
		if password, err = auth.RandomPassword(); err != nil {
			return err
		}
		generated = true
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("ADMIN_PASSWORD: %w", err)
	}
	if _, err := st.CreateUser(ctx, username, hash, auth.RoleAdmin); err != nil {
		return fmt.Errorf("create initial admin: %w", err)
	}
	if generated {
		slog.Warn("created initial admin with a generated password; sign in and change it",
			"username", username, "password", password)
	} else {
		slog.Info("created initial admin from ADMIN_USERNAME and ADMIN_PASSWORD", "username", username)
	}
	return nil
}
