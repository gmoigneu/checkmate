// Command checkmate is the Checkmate API server and its operational CLI.
//
//	checkmate serve
//	checkmate migrate up|down|status
//	checkmate user create -email you@example.com -name "Your Name"
//	checkmate token create -email you@example.com -name "iOS"
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nls/checkmate/server/internal/account"
	"github.com/nls/checkmate/server/internal/config"
	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/httpapi"
)

// version is overridden at build time:
//
//	go build -ldflags "-X main.version=$(git describe --tags --always)"
var version = "dev"

const usage = `checkmate - personal task management API

Usage:
  checkmate serve                      Run the HTTP API
  checkmate migrate up                 Apply pending migrations
  checkmate migrate down               Roll back the most recent migration
  checkmate migrate status             Show migration state
  checkmate user create -email E -name N [-timezone TZ]
  checkmate token create -email E -name N [-scopes "tasks:read tasks:write"]
  checkmate version                    Print the build version

Environment:
  CHECKMATE_ENV                development | production  (default development)
  CHECKMATE_ADDR               listen address            (default :8080)
  CHECKMATE_DB_PATH            sqlite file               (default checkmate.db)
  CHECKMATE_AUTO_MIGRATE       migrate on boot           (default true)
  CHECKMATE_SHUTDOWN_TIMEOUT   drain timeout             (default 15s)
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "checkmate: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)

		return errors.New("no command given")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := newLogger(cfg)

	// Signals cancel the root context, which unwinds serve and any long CLI
	// command cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "serve":
		return serve(ctx, cfg, log)
	case "migrate":
		return migrateCmd(ctx, cfg, log, args[1:])
	case "user":
		return userCmd(ctx, cfg, log, args[1:])
	case "token":
		return tokenCmd(ctx, cfg, log, args[1:])
	case "version":
		fmt.Println(version)

		return nil
	case "help", "-h", "--help":
		fmt.Print(usage)

		return nil
	default:
		fmt.Print(usage)

		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serve(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	db, err := open(ctx, cfg, log, cfg.AutoMigrate)
	if err != nil {
		return err
	}
	defer db.Close()

	schemaVersion, err := database.Version(ctx, db)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           httpapi.New(db, log, version).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	// Surface a failed Listen immediately instead of hanging on ctx.Done().
	errc := make(chan error, 1)

	go func() {
		log.Info("listening",
			slog.String("addr", cfg.Addr),
			slog.String("env", cfg.Env),
			slog.String("version", version),
			slog.String("database", cfg.DatabasePath),
			slog.Int64("schema_version", schemaVersion),
		)

		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("listen: %w", err)

			return
		}

		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", slog.Duration("timeout", cfg.ShutdownTimeout))

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}

	return <-errc
}

func migrateCmd(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("migrate needs a subcommand: up, down or status")
	}

	db, err := open(ctx, cfg, log, false)
	if err != nil {
		return err
	}
	defer db.Close()

	switch args[0] {
	case "up":
		return database.Migrate(ctx, db, log)
	case "down":
		return database.MigrateDown(ctx, db, log)
	case "status":
		return database.MigrationStatus(ctx, db, os.Stdout)
	default:
		return fmt.Errorf("unknown migrate subcommand %q", args[0])
	}
}

func userCmd(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("user needs a subcommand: create")
	}

	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	email := fs.String("email", "", "email address (required)")
	name := fs.String("name", "", "display name (required)")
	timezone := fs.String("timezone", "UTC", "IANA timezone, e.g. Europe/Paris")
	tokenName := fs.String("token", "", "also issue an API token with this name")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	db, err := open(ctx, cfg, log, cfg.AutoMigrate)
	if err != nil {
		return err
	}
	defer db.Close()

	u, err := account.CreateUser(ctx, db, *email, *name, *timezone)
	if err != nil {
		return err
	}

	fmt.Printf("created user %s <%s>\n", u.ID, u.Email)
	fmt.Printf("seeded contexts: ")

	for i, c := range account.DefaultContexts {
		if i > 0 {
			fmt.Print(", ")
		}

		fmt.Print(c.Name)
	}

	fmt.Println()

	if *tokenName != "" {
		secret, err := account.CreateToken(ctx, db, u.ID, *tokenName, "")
		if err != nil {
			return err
		}

		printToken(*tokenName, secret)
	}

	return nil
}

func tokenCmd(ctx context.Context, cfg config.Config, log *slog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("token needs a subcommand: create")
	}

	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	email := fs.String("email", "", "owner's email address (required)")
	name := fs.String("name", "", "token name, e.g. iOS or hermes (required)")
	scopes := fs.String("scopes", "", "space-separated scopes (default \"tasks:read tasks:write\")")

	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	db, err := open(ctx, cfg, log, false)
	if err != nil {
		return err
	}
	defer db.Close()

	u, err := account.FindUserByEmail(ctx, db, *email)
	if err != nil {
		return err
	}

	secret, err := account.CreateToken(ctx, db, u.ID, *name, *scopes)
	if err != nil {
		return err
	}

	printToken(*name, secret)

	return nil
}

func printToken(name, secret string) {
	fmt.Printf("token %q: %s\n", name, secret)
	fmt.Println("store it now - only its hash is kept, so it cannot be shown again")
}

// open connects to the database and optionally migrates it.
func open(ctx context.Context, cfg config.Config, log *slog.Logger, migrate bool) (*sql.DB, error) {
	db, err := database.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return nil, err
	}

	if migrate {
		if err := database.Migrate(ctx, db, log); err != nil {
			db.Close()

			return nil, err
		}
	}

	return db, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}

	if cfg.Development() {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}

	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}
