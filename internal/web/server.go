// Package web — тонкий BFF плана окна: проверить токен, разобрать запрос, вызвать
// plan.Service, отдать JSON и статику. Деньги тут не считаются и не форматируются.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"simpleAI/internal/plan"
)

//go:embed static
var staticFS embed.FS

// Planner — то, что вебу нужно от plan.Service (ISP).
type Planner interface {
	ViewWith(ctx context.Context, cur plan.Currency, opt plan.ViewOptions) (plan.Result, error)
	AddIncome(ctx context.Context, in plan.AddIncomeInput) (string, error)
	AddPayment(ctx context.Context, in plan.AddPaymentInput) (string, error)
	Delete(ctx context.Context, key string) error
	Settle(ctx context.Context, key string, actual *plan.Money) error
	Skip(ctx context.Context, key string) error
	Move(ctx context.Context, key string, in plan.MoveInput) error
	SetRate(ctx context.Context, rubPerThbMicro int64) error
	ExpenseCategories(ctx context.Context) ([]string, error)
	Item(ctx context.Context, key string) (plan.EditableItem, error)
	Edit(ctx context.Context, key string, in plan.EditInput) error
	Reopen(ctx context.Context, key string) error
}

const (
	maxConcurrent  = 4
	requestTimeout = 10 * time.Second
	maxBadPerMin   = 10
)

// Server — HTTP-сервер плана окна.
type Server struct {
	cfg     Config
	planner Planner
	logger  *slog.Logger
	hosts   map[string]bool
	sem     chan struct{}
	limiter *badAttempts
	handler http.Handler
	// limits и suggester — лимиты категорий (WithLimits); nil — маршруты отвечают 404.
	limits    LimitPlanner
	suggester plan.LimitSuggester
}

// New собирает сервер. Невалидный конфиг (в том числе слабый токен) — ошибка:
// веб не должен стартовать с небезопасными настройками (fail-closed).
func New(cfg Config, p Planner, logger *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg: cfg, planner: p, logger: logger,
		hosts:   map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true},
		sem:     make(chan struct{}, maxConcurrent),
		limiter: &badAttempts{now: time.Now},
	}
	for _, h := range cfg.Hosts {
		s.hosts[h] = true
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok")) //nolint:errcheck // ответ на healthz
	})
	mux.Handle("GET /api/plan/window", s.auth(http.HandlerFunc(s.handleWindow)))
	mux.Handle("GET /api/plan/categories", s.auth(http.HandlerFunc(s.handleCategories)))
	mux.Handle("POST /api/plan/income", s.auth(http.HandlerFunc(s.handleAddIncome)))
	mux.Handle("POST /api/plan/payment", s.auth(http.HandlerFunc(s.handleAddPayment)))
	mux.Handle("POST /api/plan/item/{key}/done", s.auth(http.HandlerFunc(s.handleDone)))
	mux.Handle("POST /api/plan/item/{key}/move", s.auth(http.HandlerFunc(s.handleMove)))
	mux.Handle("POST /api/plan/item/{key}/skip", s.auth(http.HandlerFunc(s.handleSkip)))
	mux.Handle("POST /api/plan/item/{key}/reopen", s.auth(http.HandlerFunc(s.handleReopen)))
	mux.Handle("POST /api/plan/rate", s.auth(http.HandlerFunc(s.handleRate)))
	mux.Handle("DELETE /api/plan/item/{key}", s.auth(http.HandlerFunc(s.handleDelete)))
	mux.Handle("GET /api/plan/item/{key}", s.auth(http.HandlerFunc(s.handleItem)))
	mux.Handle("PATCH /api/plan/item/{key}", s.auth(http.HandlerFunc(s.handleEdit)))
	s.registerLimitRoutes(mux)
	files := http.FileServerFS(sub)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Без листинга каталогов: отдаём только сами файлы.
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		files.ServeHTTP(w, r)
	})
	s.handler = s.recoverer(s.secure(s.logRequests(mux)))
	return s, nil
}

// Handler отдаёт собранный обработчик (для тестов и httptest).
func (s *Server) Handler() http.Handler { return s.handler }

// Run слушает адрес до отмены ctx. Ошибки уходят в лог, а не вызывающему: падение
// веба не должно убивать бота.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()
	s.logger.Info("plan web started", "addr", s.cfg.Addr)
	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// secure: проверка Host, заголовки безопасности, ограничение параллельных запросов.
func (s *Server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hosts[hostName(r.Host)] {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'")
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// hostName отбрасывает порт: сравниваем имя, а не имя:порт.
func hostName(h string) string {
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.ToLower(strings.Trim(h, "[]"))
}

// auth требует Authorization: Bearer <токен> на /api/*. Сравнение за постоянное
// время; считаются и ограничиваются только НЕВЕРНЫЕ попытки, верный токен лимитом
// не блокируется.
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.Header.Get("Authorization") == got || subtle.ConstantTimeCompare([]byte(got), []byte(s.cfg.Token)) != 1 {
			if !s.limiter.allow() {
				writeError(w, http.StatusTooManyRequests, "Слишком много неверных попыток. Подожди минуту.")
				return
			}
			writeError(w, http.StatusUnauthorized, "Нужен токен доступа.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// badAttempts — глобальный счётчик неверных попыток за минуту.
type badAttempts struct {
	mu    sync.Mutex
	now   func() time.Time
	times []time.Time
}

// allow фиксирует неверную попытку и сообщает, можно ли ещё отвечать 401.
func (b *badAttempts) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	cut := b.now().Add(-time.Minute)
	kept := b.times[:0]
	for _, t := range b.times {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	b.times = kept
	if len(b.times) >= maxBadPerMin {
		return false
	}
	b.times = append(b.times, b.now())
	return true
}

// statusWriter запоминает код ответа для лога.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// logRequests пишет одну строку на запрос: метод, шаблон маршрута, статус, мс.
// URL и query в лог не попадают (в них могли бы оказаться секреты).
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		route := r.Pattern
		if route == "" {
			route = "-"
		}
		s.logger.Info("plan web", "method", r.Method, "route", route, "status", sw.status, "ms", time.Since(start).Milliseconds())
	})
}

// recoverer ловит панику обработчика и отвечает 500 без деталей наружу.
func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("plan web: panic", "panic", rec)
				writeError(w, http.StatusInternalServerError, "Не удалось посчитать. Попробуй ещё раз.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck // клиент мог уйти
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v) //nolint:errcheck // клиент мог уйти
}
