package categories

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// Режимы выбора категории (ADR-012): off — как раньше, shadow — классификатор вызывается и
// логируется, но на запись не влияет, on — решает классификатор.
const (
	ModeOff    = "off"
	ModeShadow = "shadow"
	ModeOn     = "on"
)

// Источники выбора категории (для логов и ответа бота).
const (
	SourceExplicit   = "explicit"   // пользователь назвал категорию
	SourceMemory     = "memory"     // такое же описание уже записывали
	SourceClassifier = "classifier" // выбрал классификатор
	SourceDefault    = "default"    // цель по умолчанию split-источника
	SourceDegraded   = "degraded"   // не определили: «Прочее»
)

// minConfidence — ниже этого ответ классификатора считается «не определил».
const minConfidence = 0.5

// otherCategory — категория, в которую записываем, когда определить не удалось.
const otherCategory = "Прочее"

// Directory — то, что Chooser берёт у хранилища категорий (реализует *Store).
type Directory interface {
	Resolve(ctx context.Context, name, typ string) (Category, bool, error)
	GetOrCreate(ctx context.Context, name, typ string) (Category, bool, error)
	SplitTargets(ctx context.Context, source uuid.UUID) ([]SplitTarget, error)
	ListAssignable(ctx context.Context, typ string) ([]Category, error)
}

// DescriptionMemory — самая частая категория для такого же описания (реализует *Store).
type DescriptionMemory interface {
	MostUsedCategory(ctx context.Context, typ, description string) (Category, bool, error)
}

// Proposal — запись для очереди разбора: что не смогли определить и что предложила модель.
type Proposal struct {
	Type        string
	Name        string // предложенное моделью имя либо описание траты
	Description string
	Reason      string
}

// ChooseInput — вход выбора. Name — то, что назвал пользователь (может быть пустым).
type ChooseInput struct {
	Name        string
	Description string
	Type        string
}

// Choice — результат. Пустой Category.ID значит «без категории» (как раньше при пустом имени).
type Choice struct {
	Category Category
	Source   string
	// Note — что сказать пользователю, если категория определена автоматически или не определена.
	Note     string
	Proposal *Proposal
}

// Chooser выбирает категорию: имя пользователя как есть → память описаний → классификатор →
// «Прочее» с предложением в очередь (ADR-012 §3, §5). Классификатор не вызывается, если
// пользователь назвал категорию, и сбой классификатора никогда не блокирует запись.
type Chooser struct {
	Dir        Directory
	Memory     DescriptionMemory
	Classifier Classifier // nil = нет классификатора
	Mode       string
	Hints      map[string]string // подсказки по именам категорий (нижний регистр)
	Log        *slog.Logger
}

func (c *Chooser) logger() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.Default()
}

func (c *Chooser) classifierOn() bool { return c.Mode == ModeOn && c.Classifier != nil }

// Choose — единая точка выбора категории для бота.
func (c *Chooser) Choose(ctx context.Context, in ChooseInput) (Choice, error) {
	desc := Clean(in.Description)
	if Clean(in.Name) != "" {
		return c.fromName(ctx, in, desc)
	}
	if in.Type != "expense" || desc == "" {
		return Choice{}, nil // как раньше: без имени и описания категории нет
	}
	return c.fromDescription(ctx, in, desc)
}

// fromName: названное пользователем принимается как есть; но легаси-источник разделения
// («Еда») уточняется по описанию (ADR-013 §3).
func (c *Chooser) fromName(ctx context.Context, in ChooseInput, desc string) (Choice, error) {
	cat, found, err := c.Dir.Resolve(ctx, in.Name, in.Type)
	if err != nil {
		return Choice{}, err
	}
	if !found {
		cat, _, err = c.Dir.GetOrCreate(ctx, in.Name, in.Type)
		if err != nil {
			return Choice{}, err
		}
		return Choice{Category: cat, Source: SourceExplicit}, nil
	}
	targets, err := c.Dir.SplitTargets(ctx, cat.ID)
	if err != nil {
		return Choice{}, err
	}
	if len(targets) == 0 {
		return Choice{Category: cat, Source: SourceExplicit}, nil
	}
	return c.splitChoose(ctx, in, desc, targets)
}

// splitChoose: память → классификатор среди целей → цель по умолчанию.
func (c *Chooser) splitChoose(ctx context.Context, in ChooseInput, desc string, targets []SplitTarget) (Choice, error) {
	allowed := make(map[uuid.UUID]Category, len(targets))
	cands := make([]Candidate, 0, len(targets))
	for _, t := range targets {
		allowed[t.ID] = t.Category
		cands = append(cands, c.candidate(t.Category))
	}
	if desc != "" {
		if cat, ok, err := c.fromMemory(ctx, in.Type, desc); err != nil {
			return Choice{}, err
		} else if ok {
			if _, inTargets := allowed[cat.ID]; inTargets {
				return Choice{Category: cat, Source: SourceMemory}, nil
			}
		}
		if res, ok := c.classify(ctx, desc, cands); ok && res.Action == ActionExisting && res.Confidence >= minConfidence {
			if cat, inTargets := allowed[res.CategoryID]; inTargets && c.classifierOn() {
				return Choice{Category: cat, Source: SourceClassifier}, nil
			}
		}
	}
	for _, t := range targets {
		if t.IsDefault {
			return Choice{Category: t.Category, Source: SourceDefault}, nil
		}
	}
	return Choice{Category: targets[0].Category, Source: SourceDefault}, nil
}

func (c *Chooser) fromDescription(ctx context.Context, in ChooseInput, desc string) (Choice, error) {
	if c.Mode == ModeOff || c.Mode == "" {
		return Choice{}, nil
	}
	// Память меняет запись, поэтому в shadow (запись не меняется) она не применяется.
	if c.Mode == ModeOn {
		if cat, ok, err := c.fromMemory(ctx, in.Type, desc); err != nil {
			return Choice{}, err
		} else if ok {
			return Choice{Category: cat, Source: SourceMemory}, nil
		}
	}
	cats, err := c.Dir.ListAssignable(ctx, in.Type)
	if err != nil {
		return Choice{}, err
	}
	byID := make(map[uuid.UUID]Category, len(cats))
	cands := make([]Candidate, 0, len(cats))
	for _, k := range cats {
		byID[k.ID] = k
		cands = append(cands, c.candidate(k))
	}
	res, ok := c.classify(ctx, desc, cands)
	if !c.classifierOn() {
		return Choice{}, nil // shadow: результат только в лог
	}
	if ok && res.Action == ActionExisting && res.Confidence >= minConfidence {
		if cat, known := byID[res.CategoryID]; known {
			return Choice{Category: cat, Source: SourceClassifier, Note: "категория определена автоматически"}, nil
		}
	}
	return c.degrade(ctx, in, desc, res, ok)
}

// degrade: «Прочее» и предложение в очередь; категорию из ответа модели не создаём.
func (c *Chooser) degrade(ctx context.Context, in ChooseInput, desc string, res Classification, answered bool) (Choice, error) {
	other, _, err := c.Dir.GetOrCreate(ctx, otherCategory, in.Type)
	if err != nil {
		return Choice{}, err
	}
	p := &Proposal{Type: in.Type, Name: desc, Description: desc}
	note := "не удалось определить категорию, записано в «Прочее»"
	if answered {
		p.Reason = res.Reason
		if res.Action == ActionCreate && Clean(res.Name) != "" {
			p.Name = Clean(res.Name)
			note = fmt.Sprintf("подходящей категории нет, записано в «Прочее» (предложена «%s»)", p.Name)
		}
	}
	return Choice{Category: other, Source: SourceDegraded, Note: note, Proposal: p}, nil
}

// classify вызывает классификатор, если он есть и режим не off. ok=false — вызова не было или он
// упал; сбой только логируется.
func (c *Chooser) classify(ctx context.Context, desc string, cands []Candidate) (Classification, bool) {
	if c.Classifier == nil || c.Mode == ModeOff || c.Mode == "" || len(cands) == 0 {
		return Classification{}, false
	}
	res, err := c.Classifier.Classify(ctx, desc, cands)
	if err != nil {
		reason := "ошибка"
		switch {
		case errors.Is(err, ErrClassifierUnavailable):
			reason = "предохранитель"
		case errors.Is(err, ErrClassifierBudget):
			reason = "суточный потолок"
		case errors.Is(err, ErrBadClassification):
			reason = "ответ отклонён"
		}
		c.logger().Warn("category classifier failed", "mode", c.Mode, "reason", reason)
		return Classification{}, false
	}
	// Описание в лог не пишем (личные данные): только действие, уверенность и режим.
	c.logger().Info("category classified", "mode", c.Mode, "action", res.Action, "confidence", res.Confidence, "category", res.Name)
	return res, true
}

func (c *Chooser) fromMemory(ctx context.Context, typ, desc string) (Category, bool, error) {
	if c.Memory == nil {
		return Category{}, false, nil
	}
	return c.Memory.MostUsedCategory(ctx, typ, desc)
}

func (c *Chooser) candidate(k Category) Candidate {
	return Candidate{ID: k.ID, Name: k.Name, Hint: c.Hints[strings.ToLower(k.Name)]}
}
