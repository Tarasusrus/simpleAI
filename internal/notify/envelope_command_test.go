package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"simpleAI/internal/budget"
)

func TestParseEnvelopeCommand_Enable(t *testing.T) {
	cases := []string{
		"присылай конверты по утрам",
		"Присылай конверты по утрам!",
		"шли конверты утром",
		"включи утренние конверты",
	}
	for _, in := range cases {
		cmd, ok := ParseEnvelopeCommand(in)
		if !ok || !cmd.Enable {
			t.Fatalf("%q: want enable, got ok=%v cmd=%+v", in, ok, cmd)
		}
		if cmd.HasTime {
			t.Fatalf("%q: time not named, got %+v", in, cmd)
		}
	}
}

func TestParseEnvelopeCommand_Disable(t *testing.T) {
	cases := []string{
		"не присылай конверты",
		"не шли конверты по утрам",
		"выключи утренние конверты",
		"отключи конверты по утрам",
	}
	for _, in := range cases {
		cmd, ok := ParseEnvelopeCommand(in)
		if !ok {
			t.Fatalf("%q: not recognized", in)
		}
		if cmd.Enable {
			t.Fatalf("%q: want disable, got %+v", in, cmd)
		}
	}
}

func TestParseEnvelopeCommand_WithTime(t *testing.T) {
	cmd, ok := ParseEnvelopeCommand("присылай конверты по утрам в 7:30")
	if !ok || !cmd.Enable || !cmd.HasTime {
		t.Fatalf("want enable with time, got ok=%v cmd=%+v", ok, cmd)
	}
	if cmd.Hour != 7 || cmd.Minute != 30 {
		t.Fatalf("want 07:30, got %02d:%02d", cmd.Hour, cmd.Minute)
	}

	cmd, ok = ParseEnvelopeCommand("присылай конверты по утрам в 9")
	if !ok || cmd.Hour != 9 || cmd.Minute != 0 || !cmd.HasTime {
		t.Fatalf("want 09:00, got ok=%v cmd=%+v", ok, cmd)
	}
}

func TestParseEnvelopeCommand_Unrelated(t *testing.T) {
	cases := []string{
		"покажи конверты",
		"купил еды на 300",
		"присылай напоминания по утрам",
		"",
	}
	for _, in := range cases {
		if cmd, ok := ParseEnvelopeCommand(in); ok {
			t.Fatalf("%q: must not be an envelope-schedule command, got %+v", in, cmd)
		}
	}
}

type fakeEnvelopeReminderStore struct {
	get    *budget.Reminder
	getErr error
	saved  *budget.Reminder
}

func (f *fakeEnvelopeReminderStore) GetReminder(_ context.Context, _ int64) (*budget.Reminder, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.get, nil
}

func (f *fakeEnvelopeReminderStore) SetEnvelopeReminder(_ context.Context, r budget.Reminder) error {
	cp := r
	f.saved = &cp
	return nil
}

// Настройки нет вовсе → дефолт 08:00 в поясе оператора.
func TestApplyEnvelopeCommand_EnableDefaults(t *testing.T) {
	store := &fakeEnvelopeReminderStore{getErr: fmt.Errorf("get reminder: %w", pgx.ErrNoRows)}
	cmd, _ := ParseEnvelopeCommand("присылай конверты по утрам")

	reply, err := ApplyEnvelopeCommand(context.Background(), store, 42, cmd, "Asia/Bangkok")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if store.saved == nil {
		t.Fatal("nothing saved")
	}
	got := *store.saved
	if got.ChatID != 42 || !got.EnvelopeEnabled || got.EnvelopeHour != DefaultEnvelopeHour || got.Timezone != "Asia/Bangkok" {
		t.Fatalf("unexpected saved settings: %+v", got)
	}
	if !strings.Contains(reply, "08:00") {
		t.Fatalf("reply must name the time: %q", reply)
	}
}

// Пояс уже сохранён — команда его не затирает.
func TestApplyEnvelopeCommand_KeepsStoredTimezone(t *testing.T) {
	store := &fakeEnvelopeReminderStore{get: &budget.Reminder{
		ChatID: 42, Enabled: true, NotifyHour: 21, Timezone: "Europe/Moscow",
	}}
	cmd, _ := ParseEnvelopeCommand("присылай конверты по утрам в 7:30")

	if _, err := ApplyEnvelopeCommand(context.Background(), store, 42, cmd, "Asia/Bangkok"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := *store.saved
	if got.Timezone != "Europe/Moscow" {
		t.Fatalf("stored timezone overwritten: %+v", got)
	}
	if got.EnvelopeHour != 7 || got.EnvelopeMinute != 30 {
		t.Fatalf("want 07:30, got %+v", got)
	}
	if !got.Enabled || got.NotifyHour != 21 {
		t.Fatalf("evening reminder must survive: %+v", got)
	}
}

// Выключение не трогает время и вечернее напоминание.
func TestApplyEnvelopeCommand_Disable(t *testing.T) {
	store := &fakeEnvelopeReminderStore{get: &budget.Reminder{
		ChatID: 42, Timezone: "Asia/Bangkok",
		EnvelopeEnabled: true, EnvelopeHour: 7, EnvelopeMinute: 30,
	}}
	cmd, _ := ParseEnvelopeCommand("не присылай конверты")

	reply, err := ApplyEnvelopeCommand(context.Background(), store, 42, cmd, "Asia/Bangkok")
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := *store.saved
	if got.EnvelopeEnabled {
		t.Fatalf("want disabled, got %+v", got)
	}
	if got.EnvelopeHour != 7 || got.EnvelopeMinute != 30 {
		t.Fatalf("time must be preserved on disable: %+v", got)
	}
	if strings.TrimSpace(reply) == "" {
		t.Fatal("reply must not be empty")
	}
}

// Временная ошибка БД — НЕ повод считать, что настройки нет: запись дефолтов
// поверх живой строки стирает вечернее напоминание оператора.
func TestApplyEnvelopeCommand_DBErrorDoesNotOverwrite(t *testing.T) {
	boom := errors.New("connection refused")
	store := &fakeEnvelopeReminderStore{getErr: boom}
	cmd, _ := ParseEnvelopeCommand("присылай конверты по утрам")

	reply, err := ApplyEnvelopeCommand(context.Background(), store, 42, cmd, "Asia/Bangkok")
	if !errors.Is(err, boom) {
		t.Fatalf("ожидалась ошибка чтения, получено err=%v reply=%q", err, reply)
	}
	if store.saved != nil {
		t.Fatalf("при ошибке чтения ничего писать нельзя, записано: %+v", *store.saved)
	}
}

// Строки действительно нет (ErrNoRows) — первое включение обязано её создать.
func TestApplyEnvelopeCommand_NoRowsCreatesSettings(t *testing.T) {
	store := &fakeEnvelopeReminderStore{getErr: fmt.Errorf("get reminder: %w", pgx.ErrNoRows)}
	cmd, _ := ParseEnvelopeCommand("присылай конверты по утрам")

	if _, err := ApplyEnvelopeCommand(context.Background(), store, 42, cmd, "Asia/Bangkok"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if store.saved == nil {
		t.Fatal("первое включение должно создать настройку")
	}
	if got := *store.saved; !got.EnvelopeEnabled || got.EnvelopeHour != DefaultEnvelopeHour {
		t.Fatalf("unexpected saved settings: %+v", got)
	}
}
