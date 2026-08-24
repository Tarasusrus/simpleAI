package db

import (
	"strings"
	"testing"
)

// Goose ключует применённые миграции по НОМЕРУ версии, а не по имени файла.
// Два файла с одним номером — молчаливая потеря одной из них на проде:
// применится первая, вторая навсегда останется «уже применённой».
func TestMigrations_VersionsAreUnique(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, _, ok := strings.Cut(name, "_")
		if !ok {
			t.Fatalf("миграция без номера версии: %s", name)
		}
		if prev, dup := seen[version]; dup {
			t.Fatalf("дубль версии %s: %s и %s", version, prev, name)
		}
		seen[version] = name
	}
	if len(seen) == 0 {
		t.Fatal("миграции не найдены — тест ничего не проверяет")
	}
}
