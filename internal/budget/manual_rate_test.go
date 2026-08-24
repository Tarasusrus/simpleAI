package budget

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func rateTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	url := os.Getenv("BOTCLIENT_DATABASE_URL_RW")
	if url == "" {
		t.Skip("BOTCLIENT_DATABASE_URL_RW не задан — write-доступ к реплике недоступен")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return &Store{pool: pool}, ctx
}

// Ручной курс обязан пережить суточный тик воркера (simpleAI-su6l).
//
// Это главный гейт задачи: положи ручной курс в ту же колонку, что пишет
// воркер, — и он молча исчезнет в ближайшие 24 часа. Оператор увидит, что
// команда «сработала», а через сутки конверты снова посчитаются по межбанку.
//
// Мутация: убрать COALESCE из GetExchangeRates либо начать писать manual в
// SaveExchangeRate — тест краснеет.
func TestManualRate_SurvivesWorkerTick(t *testing.T) {
	s, ctx := rateTestStore(t)
	const cur = "THB"

	before, existed, err := s.GetRateSource(ctx, cur)
	if err != nil {
		t.Fatalf("read rate source: %v", err)
	}
	t.Cleanup(func() {
		// Вернуть реплику в исходное состояние: тест ходит в живую базу.
		// Ошибки восстановления не глушим — незамеченный сбой оставит реплику с
		// чужим курсом, и следующий тест упадёт непонятно почему.
		restore := func(err error) {
			if err != nil {
				t.Errorf("не удалось вернуть реплику в исходное состояние: %v", err)
			}
		}
		if existed && before.Manual {
			restore(s.SetManualRate(ctx, cur, before.RateToRUB))
			return
		}
		restore(s.ClearManualRate(ctx, cur))
		if existed {
			restore(s.SaveExchangeRate(ctx, cur, before.Auto))
		}
	})

	if err := s.SetManualRate(ctx, cur, 2.7); err != nil {
		t.Fatalf("set manual rate: %v", err)
	}

	rates, err := s.GetExchangeRates(ctx)
	if err != nil {
		t.Fatalf("get rates: %v", err)
	}
	if got := rates[cur]; got != 2.7 {
		t.Fatalf("действующий курс %v, ожидался ручной 2,7", got)
	}

	// Тик воркера: тот же вызов, что делает rates.Worker раз в сутки.
	if err := s.SaveExchangeRate(ctx, cur, 2.5351); err != nil {
		t.Fatalf("worker tick: %v", err)
	}

	rates, err = s.GetExchangeRates(ctx)
	if err != nil {
		t.Fatalf("get rates after tick: %v", err)
	}
	if got := rates[cur]; got != 2.7 {
		t.Errorf("после тика воркера курс %v — ручной курс затёрт автоматическим", got)
	}

	src, ok, err := s.GetRateSource(ctx, cur)
	if err != nil || !ok {
		t.Fatalf("read rate source after tick: err=%v ok=%v", err, ok)
	}
	if !src.Manual {
		t.Error("курс перестал считаться ручным")
	}
	if src.Auto != 2.5351 {
		t.Errorf("автокурс %v — воркер обязан продолжать его обновлять и под override'ом", src.Auto)
	}

	// «курс авто» возвращает то, что за это время принёс воркер.
	if err := s.ClearManualRate(ctx, cur); err != nil {
		t.Fatalf("clear manual rate: %v", err)
	}
	rates, err = s.GetExchangeRates(ctx)
	if err != nil {
		t.Fatalf("get rates after clear: %v", err)
	}
	if got := rates[cur]; got != 2.5351 {
		t.Errorf("после «курс авто» действует %v, ожидался автокурс 2,5351", got)
	}
}

// Повторный «курс авто» — не ошибка: оператору важно состояние «ручного курса
// нет», а не факт удаления строки.
func TestManualRate_ClearIsIdempotent(t *testing.T) {
	s, ctx := rateTestStore(t)
	const cur = "THB"

	before, existed, err := s.GetRateSource(ctx, cur)
	if err != nil {
		t.Fatalf("read rate source: %v", err)
	}
	t.Cleanup(func() {
		if existed && before.Manual {
			if err := s.SetManualRate(ctx, cur, before.RateToRUB); err != nil {
				t.Errorf("не удалось вернуть ручной курс: %v", err)
			}
		}
	})

	if err := s.ClearManualRate(ctx, cur); err != nil {
		t.Fatalf("first clear: %v", err)
	}
	if err := s.ClearManualRate(ctx, cur); err != nil {
		t.Errorf("повторный «курс авто» вернул ошибку: %v", err)
	}
}

// Курс — величина строго положительная: нулевой делит на ноль в ToTHB,
// отрицательный печатает отрицательные конверты.
func TestManualRate_RejectsNonPositive(t *testing.T) {
	s, ctx := rateTestStore(t)
	for _, bad := range []float64{0, -2.7} {
		if err := s.SetManualRate(ctx, "THB", bad); err == nil {
			t.Errorf("курс %v принят, ожидалась ошибка", bad)
		}
	}
}
