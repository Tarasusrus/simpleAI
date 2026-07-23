package botclient

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestReadOnlyPoolRejectsWrite доказывает enforcement на уровне БД: пул,
// собранный из BOTCLIENT_DATABASE_URL (роль botclient_ro), физически не может
// писать. Запускается только когда задан BOTCLIENT_DATABASE_URL (локальная
// реплика поднята); иначе пропускается, чтобы не ломать обычный CI.
//
// Прогон:
//
//	set -a; . ~/.simpleai-replica/botclient.env; set +a
//	go test ./internal/botclient/ -run TestReadOnlyPoolRejectsWrite -v
//
// Мутация: если default-режим начнёт отдавать write-роль, тест краснеет.
func TestReadOnlyPoolRejectsWrite(t *testing.T) {
	url := os.Getenv("BOTCLIENT_DATABASE_URL")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL не задан — реплика не поднята, пропускаю integration-проверку")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect read-only pool: %v", err)
	}
	defer pool.Close()

	// SELECT должен работать.
	var n int
	if err := pool.QueryRow(ctx, "select count(*) from budget_transaction").Scan(&n); err != nil {
		t.Fatalf("read-only SELECT должен работать, got: %v", err)
	}

	// Любая запись — отклонена ролью.
	if _, err := pool.Exec(ctx, "create table botclient_probe(x int)"); err == nil {
		t.Fatal("ожидалось permission denied на read-only пуле, но запись прошла")
	} else if !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("ожидалось 'permission denied', got: %v", err)
	}
}
