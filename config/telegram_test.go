package config

import "testing"

func TestParseChatIDsStrict(t *testing.T) {
	got, err := parseChatIDsStrict("12345, 6789")
	if err != nil || len(got) != 2 || got[0] != 12345 || got[1] != 6789 {
		t.Fatalf("valid list: got %v err %v", got, err)
	}
	if got, err := parseChatIDsStrict("  "); err != nil || got != nil {
		t.Fatalf("empty: got %v err %v", got, err)
	}
	for _, bad := range []string{"12345;6789", "12345,abc", "1,2," + "x"} {
		if _, err := parseChatIDsStrict(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestLoadTelegramBotConfigRejectsTypo(t *testing.T) {
	t.Setenv("TELEGRAM_ALLOWED_CHATS", "12345;6789")
	if _, err := loadTelegramBotConfig(); err == nil {
		t.Fatal("typo must fail config load")
	}
	t.Setenv("TELEGRAM_ALLOWED_CHATS", "12345")
	t.Setenv("ALLOW_ALL", "true")
	cfg, err := loadTelegramBotConfig()
	if err != nil || len(cfg.AllowedChats) != 1 || !cfg.AllowAll {
		t.Fatalf("got %+v err %v", cfg, err)
	}
}
