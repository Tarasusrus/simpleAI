package config

import (
	"testing"
	"time"
)

func setReceiptEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range []string{"VISION_LLM_BASE_URL", "VISION_LLM_API_KEY", "VISION_MODEL",
		"RECEIPT_MAX_MB", "VISION_TIMEOUT_SECONDS", "VISION_CATALOG_TTL_SECONDS"} {
		t.Setenv(k, env[k])
	}
}

func TestLoadReceiptConfig_WhenKeysMissing_ShouldBeDisabled(t *testing.T) {
	tests := []map[string]string{
		{},
		{"VISION_LLM_BASE_URL": "http://proxy:4444/v1"},
		{"VISION_LLM_API_KEY": "k"},
		{"VISION_LLM_BASE_URL": "  ", "VISION_LLM_API_KEY": "k"},
	}
	for _, env := range tests {
		setReceiptEnv(t, env)
		if loadReceiptConfig().Enabled() {
			t.Errorf("env %v: Enabled() = true, want false", env)
		}
	}
}

func TestLoadReceiptConfig_Defaults(t *testing.T) {
	setReceiptEnv(t, map[string]string{"VISION_LLM_BASE_URL": "http://proxy:4444/v1", "VISION_LLM_API_KEY": "k"})
	c := loadReceiptConfig()
	if !c.Enabled() {
		t.Fatal("Enabled() = false with URL and key")
	}
	if c.Model != "vision" {
		t.Errorf("Model = %q, want vision", c.Model)
	}
	if c.MaxBytes != 10<<20 {
		t.Errorf("MaxBytes = %d, want 10 MiB", c.MaxBytes)
	}
	if c.Timeout != 90*time.Second || c.CatalogTTL != 300*time.Second {
		t.Errorf("Timeout = %v, CatalogTTL = %v", c.Timeout, c.CatalogTTL)
	}
}

func TestLoadReceiptConfig_Overrides(t *testing.T) {
	setReceiptEnv(t, map[string]string{
		"VISION_LLM_BASE_URL": "http://proxy:4444/v1", "VISION_LLM_API_KEY": "k",
		"VISION_MODEL": "gemini-3.5-flash-lite", "RECEIPT_MAX_MB": "3",
		"VISION_TIMEOUT_SECONDS": "15", "VISION_CATALOG_TTL_SECONDS": "60",
	})
	c := loadReceiptConfig()
	if c.Model != "gemini-3.5-flash-lite" || c.MaxBytes != 3<<20 ||
		c.Timeout != 15*time.Second || c.CatalogTTL != time.Minute {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadReceiptConfig_WhenMaxMBInvalid_ShouldUseDefault(t *testing.T) {
	for _, raw := range []string{"0", "-5", "abc"} {
		setReceiptEnv(t, map[string]string{"RECEIPT_MAX_MB": raw})
		if got := loadReceiptConfig().MaxBytes; got != 10<<20 {
			t.Errorf("RECEIPT_MAX_MB=%q: MaxBytes = %d, want default", raw, got)
		}
	}
}
