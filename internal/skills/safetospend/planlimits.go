package safetospend

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"simpleAI/internal/botlimits"
)

// Лимиты и «Свободно» из ядра плана (ADR-016). Скилл ничего не считает: числа приходят готовыми
// из botlimits.Snapshot (plan.View), здесь только вёрстка.

// LimitsMode — как скилл использует план.
type LimitsMode struct {
	// FromPlan — отвечать числами плана (BOT_LIMITS_FROM_PLAN). Выключено: старый путь по долям конверта.
	FromPlan bool
	// Shadow — при выключенном FromPlan считать и по плану, расхождение писать в лог; пользователю не показывается.
	Shadow bool
}

// WithLimits подключает источник лимитов из плана. Без него скилл работает только по старому пути.
func (s *SafeToSpendSkill) WithLimits(r botlimits.Reader, m LimitsMode) *SafeToSpendSkill {
	s.limits, s.limitsMode = r, m
	return s
}

// planSnapshot читает план для чата. ok=false — отвечать старым путём: чат вне области, окна нет или план
// недоступен (ошибка плана не должна превращать ответ бота в ошибку, пока старый путь жив).
func (s *SafeToSpendSkill) planSnapshot(ctx context.Context, chatID int64, cur string) (botlimits.Snapshot, bool) {
	if s.limits == nil {
		return botlimits.Snapshot{}, false
	}
	snap, ok, err := s.limits.Snapshot(ctx, chatID, cur)
	if err != nil {
		s.logger.WarnContext(ctx, "safe_to_spend: план недоступен, старый путь", "err", err, "chat_id", chatID)
		return botlimits.Snapshot{}, false
	}
	return snap, ok
}

// planFirst — ответ из плана, если включён FromPlan и план отвечает; иначе "" и старый путь.
func (s *SafeToSpendSkill) planFirst(ctx context.Context, chatID int64, cur string, render func(botlimits.Snapshot) string) (string, bool) {
	if !s.limitsMode.FromPlan {
		return "", false
	}
	snap, ok := s.planSnapshot(ctx, chatID, cur)
	if !ok {
		return "", false
	}
	s.logger.InfoContext(ctx, "safe_to_spend.plan", "chat_id", chatID, "cur", cur, "can_spend", snap.CanSpend, "limits", len(snap.Limits))
	return render(snap), true
}

// ShadowDiff — расхождение старого и нового пути по одной категории или итогу.
type ShadowDiff struct {
	Category string // пусто — итог («на день»)
	Old, New int64  // целые единицы THB
}

// shadowDiff сравнивает остатки по долям конверта (старый путь, THB) с остатками плана (THB): по совпавшему имени
// категории/доли и «на день». Категории без пары не сравниваются: модели разные (набор категорий, ADR-016 §5).
func shadowDiff(items []ShareRemaining, oldDaily float64, snap botlimits.Snapshot) []ShadowDiff {
	var out []ShadowDiff
	for _, it := range items {
		l, ok := snap.ForCategory(it.Name)
		if !ok {
			continue
		}
		old := int64(roundInt(it.Remaining))
		cur := l.Left - l.Over // отрицательное — пробито, как в старой модели
		if old != cur {
			out = append(out, ShadowDiff{Category: normalizeLabel(it.Name), Old: old, New: cur})
		}
	}
	var pd int64
	if snap.PerDay != nil {
		pd = *snap.PerDay
	}
	if od := int64(roundInt(oldDaily)); od != pd {
		out = append(out, ShadowDiff{Old: od, New: pd})
	}
	return out
}

// shadowShares сравнивает уже посчитанный старый ответ с планом и пишет расхождение в лог. Любая ошибка плана
// только логируется: пользователь получает старый ответ (ADR-016 §8, шаг 2).
func (s *SafeToSpendSkill) shadowShares(ctx context.Context, chatID int64, items []ShareRemaining, daysLeft int) {
	if s.limits == nil || !s.limitsMode.Shadow || s.limitsMode.FromPlan {
		return
	}
	snap, ok := s.planSnapshot(ctx, chatID, "THB")
	if !ok {
		s.logger.InfoContext(ctx, "bot_limits_shadow: план не отвечает", "chat_id", chatID)
		return
	}
	diffs := shadowDiff(items, DailyLimit(FlexibleRemainingTHB(items), daysLeft), snap)
	for _, d := range diffs {
		s.logger.InfoContext(ctx, "bot_limits_shadow: расхождение", "chat_id", chatID, "category", d.Category, "old_thb", d.Old, "plan_thb", d.New)
	}
	s.logger.InfoContext(ctx, "bot_limits_shadow", "chat_id", chatID, "diffs", len(diffs), "plan_can_spend_thb", snap.CanSpend)
}

// shadowRemaining — то же для режима общего остатка: «Свободно осталось» старого пути против CanSpend плана (RUB).
func (s *SafeToSpendSkill) shadowRemaining(ctx context.Context, chatID int64, oldRemainingRUB int64) {
	if s.limits == nil || !s.limitsMode.Shadow || s.limitsMode.FromPlan {
		return
	}
	snap, ok := s.planSnapshot(ctx, chatID, "RUB")
	if !ok {
		s.logger.InfoContext(ctx, "bot_limits_shadow: план не отвечает", "chat_id", chatID)
		return
	}
	if snap.CanSpend != oldRemainingRUB {
		s.logger.InfoContext(ctx, "bot_limits_shadow: расхождение", "chat_id", chatID, "category", "", "old_rub", oldRemainingRUB, "plan_rub", snap.CanSpend)
	}
	s.logger.InfoContext(ctx, "bot_limits_shadow", "chat_id", chatID, "mode", "remaining", "plan_can_spend_rub", snap.CanSpend)
}

// formatPlanLimits печатает лимиты окна плана в том же виде, что прежние «конверты»: потрачено и осталось по
// категориям, «на день», пробитые отдельной строкой. Остаток не бывает отрицательным — пробитие печатается
// минусом из Over (ADR-016 §5). Все числа — готовые, целые, в валюте Snapshot.Cur.
func formatPlanLimits(snap botlimits.Snapshot, m Display, now time.Time) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s · осталось %d %s\n",
		snap.WindowStart.Format("02.01"), snap.WindowEnd.Format("02.01"), snap.DaysLeft, pluralDays(snap.DaysLeft))
	fmt.Fprintf(&b, "Курс %s ₽/฿ на %s\n", FmtRate(m.RubPerTHB), now.Format("02.01"))

	var over []string
	if len(snap.Limits) == 0 {
		b.WriteString("\nЛимитов на категории в этом окне нет. Скажи «на еду хватит 15000» — поставлю лимит.\n")
	} else {
		b.WriteString("\n**Что осталось**\n```\n")
		fmt.Fprintf(&b, "%s%s%s\n", padRight("", remLabelWidth), padLeft("потрачено", remSpentWidth), padLeft("осталось", remLeftWidth))
		var spentTotal, leftTotal int64
		for _, l := range snap.Limits {
			left := l.Left - l.Over
			spentTotal += l.Spent
			leftTotal += left
			fmt.Fprintf(&b, "%s%s%s\n", padRight(shareLabelWidth(l.Category, remLabelWidth), remLabelWidth),
				padLeft(m.Signed(int(l.Spent)), remSpentWidth), padLeft(m.Signed(int(left)), remLeftWidth))
			if l.Over > 0 {
				over = append(over, fmt.Sprintf("%s на %s", normalizeLabel(l.Category), m.Signed(int(l.Over))))
			}
		}
		spentStr, leftStr := m.Signed(int(spentTotal)), m.Signed(int(leftTotal))
		fmt.Fprintf(&b, "%s%s%s\n", padRight("", remLabelWidth),
			padLeft(strings.Repeat("-", utf8.RuneCountInString(spentStr)), remSpentWidth),
			padLeft(strings.Repeat("-", utf8.RuneCountInString(leftStr)), remLeftWidth))
		fmt.Fprintf(&b, "%s%s%s\n```\n", padRight("", remLabelWidth), padLeft(spentStr, remSpentWidth), padLeft(leftStr, remLeftWidth))
	}
	b.WriteString(planDayLines(snap, m))
	if len(over) > 0 {
		fmt.Fprintf(&b, "\n\n⚠️ Пробито: %s — дальше тратишь из свободных денег.", strings.Join(over, ", "))
	}
	return b.String()
}

// planDayLines — главное число и «Свободно»: одно и то же число на экране, в боте и в пуше (ADR-016, решение 2).
func planDayLines(snap botlimits.Snapshot, m Display) string {
	var b strings.Builder
	if snap.PerDay != nil {
		fmt.Fprintf(&b, "\n**На день: %s**\n", m.Signed(int(*snap.PerDay)))
	} else {
		b.WriteString("\n**На день: —** (свободных денег на оставшиеся дни нет)\n")
	}
	fmt.Fprintf(&b, "Свободно до конца окна: %s", m.Signed(int(snap.CanSpend)))
	if snap.LimitHold > 0 {
		fmt.Fprintf(&b, " (ещё %s отложено на лимиты категорий)", m.Signed(int(snap.LimitHold)))
	}
	return b.String()
}

// formatPlanFree — режим общего остатка («сколько свободно осталось») из плана.
func formatPlanFree(snap botlimits.Snapshot, m Display) string {
	var b strings.Builder
	fmt.Fprintf(&b, "🧧 Окно плана: %s — %s · осталось %d %s\n", snap.WindowStart.Format("02.01"), snap.WindowEnd.Format("02.01"), snap.DaysLeft, pluralDays(snap.DaysLeft))
	b.WriteString(strings.TrimPrefix(planDayLines(snap, m), "\n"))
	return b.String()
}
