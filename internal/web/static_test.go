package web

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// Страница не должна требовать unsafe-inline в CSP: ни inline-скриптов, ни
// style="" / <style>, ни обработчиков в атрибутах (C38).
func TestStatic_NoInlineCodeOrStyle(t *testing.T) {
	banned := []*regexp.Regexp{
		regexp.MustCompile(`(?i)<style`),
		regexp.MustCompile(`(?i)\sstyle\s*=`),
		regexp.MustCompile(`(?i)\son[a-z]+\s*=`),
		regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>[^<]+</script>`), // скрипт с телом
		regexp.MustCompile(`(?i)javascript:`),
		regexp.MustCompile(`(?i)https?://`), // внешних CDN нет
	}
	count := 0
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		count++
		b, rerr := staticFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, re := range banned {
			if re.Match(b) {
				t.Errorf("%s: запрещённый фрагмент %v", path, re)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("html-файлы не найдены — тест ничего не проверяет")
	}
}

// Текст попадает в страницу только как текст: никаких innerHTML и подобного (C39).
//
// Мутация: добавить innerHTML в любой модуль — тест краснеет.
func TestStatic_JSUsesTextOnly(t *testing.T) {
	banned := regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write|\beval\(|new Function|setAttribute\(\s*['"]on`)
	count := 0
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".js") {
			return err
		}
		count++
		b, rerr := staticFS.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if loc := banned.FindString(string(b)); loc != "" {
			t.Errorf("%s: %q — вставка разметки запрещена", path, loc)
		}
		if strings.Contains(string(b), "https://") || strings.Contains(string(b), "http://") {
			t.Errorf("%s: внешний адрес в коде", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("js-файлы не найдены — тест ничего не проверяет")
	}
}

// Страница и её ресурсы отдаются без токена (токен нужен только /api/*).
func TestStatic_ServedPublicly(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	for path, wantType := range map[string]string{
		"/":                 "text/html",
		"/app.css":          "text/css",
		"/js/main.js":       "text/javascript",
		"/js/blocks/kpi.js": "text/javascript",
	} {
		rec := do(s, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, wantType) {
			t.Errorf("%s: Content-Type %q, ждали %s", path, ct, wantType)
		}
	}
	// Все модули, на которые ссылается main.js, существуют.
	main, err := staticFS.ReadFile("static/js/main.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`from '(\./[^']+)'`).FindAllStringSubmatch(string(main), -1) {
		if _, err := staticFS.ReadFile("static/js/" + strings.TrimPrefix(m[1], "./")); err != nil {
			t.Errorf("main.js импортирует %s, файла нет", m[1])
		}
	}
}

// Каталоги статики не листаются.
func TestStatic_NoDirectoryListing(t *testing.T) {
	s, _ := newServer(t, &fakePlanner{})
	for _, p := range []string{"/js/", "/js/blocks/"} {
		if rec := do(s, http.MethodGet, p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, ждали 404", p, rec.Code)
		}
	}
}
