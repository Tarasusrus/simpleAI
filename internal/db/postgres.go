// Package db содержит код пакета db и его задачи.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"simpleAI/config"
)

func DSN(cfg config.DBConfig) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		cfg.User, cfg.Pass, cfg.Host, cfg.Port, cfg.Name,
	)
}

func NewPool(ctx context.Context, cfg config.DBConfig) (*pgxpool.Pool, error) {
	return pgxpool.New(ctx, DSN(cfg))
}
