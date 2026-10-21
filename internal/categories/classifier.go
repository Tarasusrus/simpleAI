package categories

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Действия классификатора.
const (
	ActionExisting = "existing" // выбрана категория из списка
	ActionCreate   = "create"   // предложена новая категория
	ActionNone     = "none"     // по описанию непонятно
)

// Candidate — категория, из которой классификатор выбирает. Hint — одна строка с границами
// категории (ADR-013 §1); в запрос уходит только он, имя и id.
type Candidate struct {
	ID   uuid.UUID
	Name string
	Hint string
}

// Classification — ответ классификатора на одно описание.
type Classification struct {
	Action     string
	CategoryID uuid.UUID // для existing
	Name       string    // для existing — имя из списка, для create — предложенное
	Confidence float64   // 0..1
	Reason     string
}

// Classifier выбирает категорию по описанию траты (ADR-012 §4). В запрос уходит только описание
// и список кандидатов: без сумм, дат и chat_id.
type Classifier interface {
	Classify(ctx context.Context, description string, candidates []Candidate) (Classification, error)
}

var (
	// ErrClassifierUnavailable — предохранитель открыт: после серии сбоев вызовы приостановлены.
	ErrClassifierUnavailable = errors.New("categories: классификатор приостановлен после сбоев")
	// ErrClassifierBudget — исчерпан суточный потолок вызовов.
	ErrClassifierBudget = errors.New("categories: суточный потолок вызовов классификатора исчерпан")
	// ErrBadClassification — ответ нарушает контракт (чужой id, плохое имя, потолок создания).
	ErrBadClassification = errors.New("categories: ответ классификатора отклонён")
)

// GuardConfig — потолки и предохранитель обёртки.
type GuardConfig struct {
	MaxCallsPerDay   int           // суточный потолок вызовов (по умолчанию 200)
	MaxCreatesPerDay int           // суточный потолок новых категорий (по умолчанию 2)
	BreakerFailures  int           // подряд идущих сбоев до паузы (по умолчанию 5)
	BreakerPause     time.Duration // пауза после срабатывания (по умолчанию 15 минут)
	Now              func() time.Time
}

func (c GuardConfig) withDefaults() GuardConfig {
	if c.MaxCallsPerDay <= 0 {
		c.MaxCallsPerDay = 200
	}
	if c.MaxCreatesPerDay <= 0 {
		c.MaxCreatesPerDay = 2
	}
	if c.BreakerFailures <= 0 {
		c.BreakerFailures = 5
	}
	if c.BreakerPause <= 0 {
		c.BreakerPause = 15 * time.Minute
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	return c
}

// protectedNames — имена, которые модель не вправе создавать: легаси и служебные (ADR-013).
var protectedNames = map[string]bool{"еда": true, "прочее": true, "без категории": true}

const maxNameRunes = 40

// GuardedClassifier оборачивает Classifier: проверяет ответ, держит суточные потолки и
// предохранитель. Безопасен для параллельных вызовов.
type GuardedClassifier struct {
	inner Classifier
	cfg   GuardConfig

	mu         sync.Mutex
	day        string
	calls      int
	creates    int
	failures   int
	pausedTill time.Time
}

// NewGuardedClassifier создаёт обёртку.
func NewGuardedClassifier(inner Classifier, cfg GuardConfig) *GuardedClassifier {
	return &GuardedClassifier{inner: inner, cfg: cfg.withDefaults()}
}

// rollDay сбрасывает суточные счётчики при смене суток (UTC). Вызывать под mu.
func (g *GuardedClassifier) rollDay(now time.Time) {
	if d := now.UTC().Format("2006-01-02"); d != g.day {
		g.day, g.calls, g.creates = d, 0, 0
	}
}

// reserve проверяет предохранитель и потолок вызовов и занимает один вызов.
func (g *GuardedClassifier) reserve() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.cfg.Now()
	g.rollDay(now)
	if now.Before(g.pausedTill) {
		return ErrClassifierUnavailable
	}
	if g.calls >= g.cfg.MaxCallsPerDay {
		return ErrClassifierBudget
	}
	g.calls++
	return nil
}

func (g *GuardedClassifier) recordFailure() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failures++
	if g.failures >= g.cfg.BreakerFailures {
		g.pausedTill = g.cfg.Now().Add(g.cfg.BreakerPause)
		g.failures = 0
	}
}

func (g *GuardedClassifier) recordSuccess() {
	g.mu.Lock()
	g.failures = 0
	g.mu.Unlock()
}

// reserveCreate занимает слот создания категории.
func (g *GuardedClassifier) reserveCreate() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rollDay(g.cfg.Now())
	if g.creates >= g.cfg.MaxCreatesPerDay {
		return false
	}
	g.creates++
	return true
}

// Classify вызывает inner и проверяет ответ. Сбой inner считается в предохранитель; отклонённый
// ответ — нет (сервис работает, но ответил неверно).
func (g *GuardedClassifier) Classify(ctx context.Context, description string, candidates []Candidate) (Classification, error) {
	if err := g.reserve(); err != nil {
		return Classification{}, err
	}
	res, err := g.inner.Classify(ctx, description, candidates)
	if err != nil {
		g.recordFailure()
		return Classification{}, err
	}
	g.recordSuccess()
	return g.validate(res, candidates)
}

func (g *GuardedClassifier) validate(res Classification, candidates []Candidate) (Classification, error) {
	if res.Confidence < 0 {
		res.Confidence = 0
	}
	if res.Confidence > 1 {
		res.Confidence = 1
	}
	switch res.Action {
	case ActionNone:
		return Classification{Action: ActionNone, Confidence: res.Confidence, Reason: res.Reason}, nil
	case ActionExisting:
		for _, c := range candidates {
			if c.ID == res.CategoryID {
				res.Name = c.Name
				return res, nil
			}
		}
		return Classification{}, fmt.Errorf("%w: category_id %s не из списка", ErrBadClassification, res.CategoryID)
	case ActionCreate:
		name := Clean(res.Name)
		if err := checkNewName(name, candidates); err != nil {
			return Classification{}, err
		}
		if !g.reserveCreate() {
			return Classification{}, fmt.Errorf("%w: суточный потолок новых категорий", ErrBadClassification)
		}
		res.Name = name
		return res, nil
	}
	return Classification{}, fmt.Errorf("%w: действие %q", ErrBadClassification, res.Action)
}

// checkNewName: короткое имя без « / », не защищённое и не совпадающее с уже существующей категорией.
func checkNewName(name string, candidates []Candidate) error {
	switch {
	case name == "":
		return fmt.Errorf("%w: пустое имя новой категории", ErrBadClassification)
	case len([]rune(name)) > maxNameRunes:
		return fmt.Errorf("%w: имя новой категории длиннее %d знаков", ErrBadClassification, maxNameRunes)
	case strings.Contains(name, "/"):
		return fmt.Errorf("%w: в имени новой категории «/»", ErrBadClassification)
	case protectedNames[strings.ToLower(name)]:
		return fmt.Errorf("%w: имя %q защищено", ErrBadClassification, name)
	}
	for _, c := range candidates {
		if strings.EqualFold(Clean(c.Name), name) {
			return fmt.Errorf("%w: категория %q уже есть, нужно выбрать её", ErrBadClassification, name)
		}
	}
	return nil
}
