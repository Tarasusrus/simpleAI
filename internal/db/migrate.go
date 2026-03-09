package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"simpleAI/config"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate применяет все pending миграции.
func Migrate(ctx context.Context, cfg config.DBConfig) (retErr error) {
	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.User, cfg.Pass, cfg.Host, cfg.Port, cfg.Name,
	)
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("migrate: open db: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil && retErr == nil {
			retErr = fmt.Errorf("migrate: close db: %w", err)
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("migrate: ping db: %w", err)
	}

	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrate: set dialect: %w", err)
	}

	if err := goose.UpContext(ctx, db, "migrations"); err != nil {
		return fmt.Errorf("migrate: up: %w", err)
	}
	return nil
}
