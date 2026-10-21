package categories

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Создавать и менять категории вне internal/categories нельзя (ADR-010 §2, ADR-012 §1): иначе
// снова появятся дубли «Кофе»/«кофе» и тихий NULL. Тест ищет прямой INSERT/DELETE по
// budget_category в Go-коде вне пакета. Миграции и тесты исключены: миграциям положено менять схему
// и данные, тестам нужны фикстуры.
//
// Мутация: вернуть в любой пакет, кроме categories, INSERT INTO budget_category — тест краснеет.
func TestNoDirectCategoryWritesOutsideModule(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	write := regexp.MustCompile(`(?i)\b(INSERT\s+INTO|DELETE\s+FROM|UPDATE)\s+budget_category\b[^_]`)
	var offenders []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path) //nolint:errcheck // путь под root
		if d.IsDir() {
			switch rel {
			case ".git", ".claude", "node_modules", "internal/db/migrations", "internal/categories":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if write.Match(b) {
			offenders = append(offenders, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("прямая запись в budget_category вне internal/categories: %v", offenders)
	}
}
