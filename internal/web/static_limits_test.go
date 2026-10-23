package web

import (
	"regexp"
	"strings"
	"testing"
)

// Блок лимитов и их формы подключены к экрану: файлы есть, реестр и «＋ Лимит» на месте.
//
// Мутация: убрать limits из blocks/index.js или кнопку из forms.js — тест краснеет.
func TestStatic_LimitsWired(t *testing.T) {
	read := func(p string) string {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return string(b)
	}
	idx := read("js/blocks/index.js")
	if !regexp.MustCompile(`import \* as limits from '\./limits\.js'`).MatchString(idx) ||
		!regexp.MustCompile(`export const blocks = \[[^\]]*\blimits\b[^\]]*\]`).MatchString(idx) {
		t.Error("блок limits не зарегистрирован в blocks/index.js")
	}
	if !strings.Contains(read("js/blocks/forms.js"), "'＋ Лимит'") {
		t.Error("в forms.js нет кнопки «＋ Лимит»")
	}
	for _, m := range regexp.MustCompile(`from '(\./[^']+)'`).FindAllStringSubmatch(read("js/blocks/limits.js")+read("js/blocks/limit-form.js"), -1) {
		path := "js/blocks/" + strings.TrimPrefix(m[1], "./")
		if strings.HasPrefix(m[1], "../") {
			path = "js/" + strings.TrimPrefix(m[1], "../")
		}
		if _, err := staticFS.ReadFile("static/" + path); err != nil {
			t.Errorf("импорт %s: файла нет", m[1])
		}
	}
	api := read("js/api.js")
	for _, fn := range []string{"addLimit", "editLimit", "deleteLimit", "previewLimit", "suggestLimit"} {
		if !strings.Contains(api, "export const "+fn) {
			t.Errorf("api.js: нет %s", fn)
		}
	}
}

// Блок «Не запланировано», раскрытие трат и форма «Запланировать» подключены (ADR-015 §3–6).
//
// Мутации: убрать unplanned из blocks/index.js; убрать вызов эндпоинта трат из api.js; убрать тост из main.js;
// убрать вывод «вне плана» из kpi.js — тест краснеет.
func TestStatic_UnplannedWired(t *testing.T) {
	read := func(p string) string {
		b, err := staticFS.ReadFile("static/" + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return string(b)
	}
	idx := read("js/blocks/index.js")
	if !regexp.MustCompile(`import \* as unplanned from '\./unplanned\.js'`).MatchString(idx) ||
		!regexp.MustCompile(`export const blocks = \[[^\]]*\blimits, unplanned\b[^\]]*\]`).MatchString(idx) {
		t.Error("блок unplanned не зарегистрирован сразу после limits в blocks/index.js")
	}
	api := read("js/api.js")
	for _, want := range []string{"export const dismissCategory", "export const restoreCategory", "export const getCategoryTransactions",
		"/api/plan/categories/dismissed", "/api/plan/categories/transactions"} {
		if !strings.Contains(api, want) {
			t.Errorf("api.js: нет %q", want)
		}
	}
	if !strings.Contains(read("js/main.js"), "toast: showToast") || !strings.Contains(read("js/main.js"), "reload: () => load()") {
		t.Error("main.js: в ctx нет toast/reload")
	}
	if !strings.Contains(read("js/blocks/kpi.js"), "потрачено вне плана") {
		t.Error("kpi.js: нет строки «потрачено вне плана»")
	}
	if !strings.Contains(read("js/blocks/limits.js"), "txPanel(l.category, cur)") {
		t.Error("limits.js: у лимита нет раскрытия трат")
	}
	if !strings.Contains(read("js/blocks/plan-form.js"), "'Отменить'") || !strings.Contains(read("js/blocks/plan-form.js"), "view.limitId") {
		t.Error("plan-form.js: нет тоста «Отменить» по limitId")
	}
	// CSP запрещает inline-стили и разметку из строк: новые файлы собирают узлы через h().
	for _, f := range []string{"unplanned.js", "cat-tx.js", "plan-form.js"} {
		src := read("js/blocks/" + f)
		if strings.Contains(src, "innerHTML") || strings.Contains(src, "style=") {
			t.Errorf("%s: innerHTML или style= (запрещено CSP)", f)
		}
		for _, m := range regexp.MustCompile(`from '(\./[^']+|\.\./[^']+)'`).FindAllStringSubmatch(src, -1) {
			path := "js/blocks/" + strings.TrimPrefix(m[1], "./")
			if strings.HasPrefix(m[1], "../") {
				path = "js/" + strings.TrimPrefix(m[1], "../")
			}
			if _, err := staticFS.ReadFile("static/" + path); err != nil {
				t.Errorf("%s: импорт %s — файла нет", f, m[1])
			}
		}
	}
	css := read("app.css")
	for _, sel := range []string{".toast", ".urow", ".pf-b", ".txp", ".chev"} {
		if !strings.Contains(css, sel) {
			t.Errorf("app.css: нет стиля %s", sel)
		}
	}
}
