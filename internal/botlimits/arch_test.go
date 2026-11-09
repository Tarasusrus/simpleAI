package botlimits

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Архитектурный тест единого ядра лимитов (ADR-016 §7): скиллы, бот и утренний пуш не обращаются к
// старому пути — методам budget.Store по долям и конвертам и budget.ResolveShare. Проверка по символам AST
// (селекторы, имена методов интерфейсов, объявления), а не по тексту SQL. Белый список — переходный период,
// пока старый путь жив за флагом BOT_LIMITS_FROM_PLAN: он сужается каждым шагом и должен совпадать с
// реальным использованием (лишняя запись в белом списке — тоже ошибка, иначе он не сужался бы).

// legacySymbols — старый путь: доли, конверты, ручные лимиты долей.
var legacySymbols = map[string]bool{
	"ResolveShare": true, "ReplaceShares": true, "CreateEnvelope": true, "CreateEnvelopeWithShares": true,
	"GetActiveEnvelope": true, "CreateShares": true, "ListShares": true, "SetOverride": true,
	"ListOverrides": true, "DeleteOverride": true, "SpentOutsideShares": true, "SpentByCategoryExcludingRecurring": true,
}

// scanRoots — где старый путь запрещён (кроме белого списка). Пути от корня модуля.
var scanRoots = []string{"internal/skills", "internal/bot", "internal/envelopepush"}

// legacyAllowed — файл → какие символы старого пути ему пока разрешены (переходный период, ADR-017: удаляется
// вместе со старым путём, ovvt.9). Новые строки сюда не добавляются.
var legacyAllowed = map[string][]string{
	"internal/envelopepush/source.go":             {"GetActiveEnvelope"},
	"internal/skills/budget/envelope.go":          {"CreateEnvelopeWithShares", "GetActiveEnvelope", "ListOverrides", "ListShares", "SpentByCategoryExcludingRecurring"},
	"internal/skills/budget/limits.go":            {"DeleteOverride", "GetActiveEnvelope", "ListShares", "ReplaceShares", "SetOverride"},
	"internal/skills/budget/share_warning.go":     {"GetActiveEnvelope", "ListShares", "SpentByCategoryExcludingRecurring"},
	"internal/skills/safetospend/compute.go":      {"ResolveShare"},
	"internal/skills/safetospend/remaining.go":    {"GetActiveEnvelope", "ListShares", "SpentByCategoryExcludingRecurring"},
	"internal/skills/safetospend/share_lookup.go": {"ResolveShare"},
	"internal/skills/safetospend/skill.go":        {"GetActiveEnvelope", "ListShares", "SpentByCategoryExcludingRecurring"},
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod не найден")
		}
		dir = parent
	}
}

// legacyUses возвращает файл → символы старого пути, встречающиеся в нетестовых файлах roots.
func legacyUses(t *testing.T, root string, roots []string) map[string]map[string]bool {
	t.Helper()
	uses := map[string]map[string]bool{}
	for _, r := range roots {
		err := filepath.Walk(filepath.Join(root, r), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path) //nolint:errcheck // путь заведомо внутри root
			note := func(name string) {
				if legacySymbols[name] {
					if uses[rel] == nil {
						uses[rel] = map[string]bool{}
					}
					uses[rel][name] = true
				}
			}
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.SelectorExpr:
					note(x.Sel.Name)
				case *ast.FuncDecl:
					note(x.Name.Name)
				case *ast.InterfaceType:
					for _, m := range x.Methods.List {
						for _, nm := range m.Names {
							note(nm.Name)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return uses
}

func TestArch_LegacyLimitsPathOnlyInWhitelist(t *testing.T) {
	root := moduleRoot(t)
	uses := legacyUses(t, root, scanRoots)
	if len(uses) == 0 && len(legacyAllowed) > 0 {
		t.Fatal("сканирование ничего не нашло — тест ничего не проверяет")
	}
	files := make([]string, 0, len(uses))
	for f := range uses {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		allowed := map[string]bool{}
		for _, s := range legacyAllowed[f] {
			allowed[s] = true
		}
		for s := range uses[f] {
			if !allowed[s] {
				t.Errorf("%s: символ старого пути %s вне белого списка (ADR-016 §7): читай и пиши лимиты через botlimits", f, s)
			}
		}
	}
	// белый список не должен отставать от кода: исчезнувшее использование убирается из него
	for f, syms := range legacyAllowed {
		for _, s := range syms {
			if !uses[f][s] {
				t.Errorf("белый список: %s больше не использует %s — убери запись", f, s)
			}
		}
	}
}

// Сам сканер ловит каждый вид использования: иначе зелёный тест мог бы означать «ничего не нашёл».
func TestArch_ScannerFindsAllUseKinds(t *testing.T) {
	dir := t.TempDir()
	src := `package x
type store interface { ListShares() }
func use(s any) { _ = budget.ResolveShare; s.SetOverride() }
func (z *T) ReplaceShares() {}
`
	if err := os.MkdirAll(filepath.Join(dir, "internal", "skills", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "skills", "x", "x.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got := legacyUses(t, dir, []string{"internal/skills"})["internal/skills/x/x.go"]
	for _, want := range []string{"ListShares", "ResolveShare", "SetOverride", "ReplaceShares"} {
		if !got[want] {
			t.Errorf("сканер не нашёл %s (kind), got=%v", want, got)
		}
	}
	if len(got) != 4 {
		t.Errorf("лишние находки: %v", got)
	}
}
