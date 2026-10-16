package plan

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Пакет plan — чистый домен. Этот тест не даёт ему обрасти зависимостями
// (база, HTTP, бот, чужие пакеты проекта) и скрытым чтением часов: тогда расчёт
// перестанет быть воспроизводимым, а экран и бот разойдутся в «сегодня».

var allowedImports = map[string]bool{
	"errors": true, "fmt": true, "sort": true, "slices": true, "strings": true,
	"math": true, "math/bits": true, "time": true,
	// context нужен только портам (ports.go): интерфейс Reader принимает контекст.
	"context": true,
	// unicode/utf8 — проверка длины названия в портах.
	"unicode/utf8": true,
}

// goFiles возвращает НЕ тестовые файлы пакета.
func goFiles(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range all {
		if !strings.HasSuffix(f, "_test.go") {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		t.Fatal("в пакете нет файлов — тест архитектуры ничего не проверяет")
	}
	return out
}

func parse(t *testing.T, file string) *ast.File {
	t.Helper()
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(token.NewFileSet(), file, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return f
}

func TestArch_OnlyAllowedImports(t *testing.T) {
	for _, file := range goFiles(t) {
		for _, imp := range parse(t, file).Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if !allowedImports[path] {
				t.Errorf("%s импортирует %q: домен plan не вправе зависеть от него", file, path)
			}
		}
	}
}

// time нужен только DateOf (date.go); остальные файлы работают с Date.
func TestArch_TimeOnlyInDateOf(t *testing.T) {
	for _, file := range goFiles(t) {
		if file == "date.go" {
			continue
		}
		for _, imp := range parse(t, file).Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if path == "time" {
				t.Errorf("%s импортирует time: время допустимо только в date.go (DateOf)", file)
			}
		}
	}
}

// Часы домен читать не вправе: «сегодня» приходит параметром.
func TestArch_NoClockReads(t *testing.T) {
	banned := map[string]bool{"Now": true, "Since": true, "Until": true}
	for _, file := range goFiles(t) {
		ast.Inspect(parse(t, file), func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "time" && banned[sel.Sel.Name] {
				t.Errorf("%s: time.%s читает часы — «сегодня» должно приходить параметром", file, sel.Sel.Name)
			}
			return true
		})
	}
}

// fmt допустим только для ошибок (Errorf): форматирование денег и текста живёт в BFF.
func TestArch_FmtOnlyForErrors(t *testing.T) {
	for _, file := range goFiles(t) {
		ast.Inspect(parse(t, file), func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if x, ok := sel.X.(*ast.Ident); ok && x.Name == "fmt" && sel.Sel.Name != "Errorf" {
				t.Errorf("%s: fmt.%s — в домене fmt только для ошибок (Errorf)", file, sel.Sel.Name)
			}
			return true
		})
	}
}
