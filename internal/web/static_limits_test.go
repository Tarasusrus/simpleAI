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
