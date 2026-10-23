package config

import (
	"testing"
	"time"
)

func setClassifierEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"CATEGORY_CLASSIFIER_MODE", "CATEGORY_LLM_BASE_URL", "CATEGORY_LLM_API_KEY",
		"CATEGORY_LLM_MODEL", "CATEGORY_LLM_TIMEOUT_SECONDS", "CATEGORY_LLM_MAX_CALLS_PER_DAY", "CATEGORY_LLM_MAX_CREATES_PER_DAY"} {
		t.Setenv(k, env[k])
	}
}

// Классификатор включается только явным режимом И ключами прокси; опечатка в режиме = выключен.
//
// Мутация: по умолчанию вернуть ClassifierOn или убрать проверку ключей — тест краснеет.
func TestLoadCategoryClassifierConfig_EnabledOnlyWithModeAndKeys(t *testing.T) {
	keys := map[string]string{"CATEGORY_LLM_BASE_URL": "http://proxy:4444/v1", "CATEGORY_LLM_API_KEY": "k"}
	with := func(mode string) map[string]string {
		m := map[string]string{"CATEGORY_CLASSIFIER_MODE": mode}
		for k, v := range keys {
			m[k] = v
		}
		return m
	}
	tests := []struct {
		name string
		env  map[string]string
		mode string
		on   bool
	}{
		{"пусто", map[string]string{}, ClassifierOff, false},
		{"режим on без ключей", map[string]string{"CATEGORY_CLASSIFIER_MODE": "on"}, ClassifierOn, false},
		{"ключи без режима", keys, ClassifierOff, false},
		{"режим off с ключами", with("off"), ClassifierOff, false},
		{"опечатка в режиме", with("onn"), ClassifierOff, false},
		{"shadow", with(" Shadow "), ClassifierShadow, true},
		{"on", with("on"), ClassifierOn, true},
	}
	for _, tc := range tests {
		setClassifierEnv(t, tc.env)
		got := loadCategoryClassifierConfig()
		if got.Mode != tc.mode || got.Enabled() != tc.on {
			t.Errorf("%s: mode=%q enabled=%v, want %q %v", tc.name, got.Mode, got.Enabled(), tc.mode, tc.on)
		}
	}
}

func TestLoadCategoryClassifierConfig_Defaults(t *testing.T) {
	setClassifierEnv(t, nil)
	got := loadCategoryClassifierConfig()
	if got.Model != "standard" || got.Timeout != 10*time.Second || got.MaxCallsPerDay != 200 || got.MaxCreatesPerDay != 2 {
		t.Errorf("defaults: %+v", got)
	}
	setClassifierEnv(t, map[string]string{"CATEGORY_LLM_MODEL": "x", "CATEGORY_LLM_TIMEOUT_SECONDS": "9",
		"CATEGORY_LLM_MAX_CALLS_PER_DAY": "50", "CATEGORY_LLM_MAX_CREATES_PER_DAY": "1"})
	got = loadCategoryClassifierConfig()
	if got.Model != "x" || got.Timeout != 9*time.Second || got.MaxCallsPerDay != 50 || got.MaxCreatesPerDay != 1 {
		t.Errorf("override: %+v", got)
	}
}
