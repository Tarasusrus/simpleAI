package budgetskill

import (
	"context"

	"simpleAI/internal/core"
)

type ctxButtonsKey struct{}

// ButtonSink — mutable container для кнопок, пробрасывается через context.
type ButtonSink struct {
	Rows [][]core.Button
}

// WithButtonSink добавляет sink в context. Вызывается из HandleDefault перед agent call.
func WithButtonSink(ctx context.Context, sink *ButtonSink) context.Context {
	return context.WithValue(ctx, ctxButtonsKey{}, sink)
}

// StoreButtons записывает кнопки в sink если он есть в context.
func StoreButtons(ctx context.Context, rows [][]core.Button) {
	if sink, ok := ctx.Value(ctxButtonsKey{}).(*ButtonSink); ok && sink != nil {
		sink.Rows = rows
	}
}

// ButtonsFromContext читает кнопки из sink в context.
func ButtonsFromContext(ctx context.Context) ([][]core.Button, bool) {
	sink, ok := ctx.Value(ctxButtonsKey{}).(*ButtonSink)
	if !ok || sink == nil || len(sink.Rows) == 0 {
		return nil, false
	}
	return sink.Rows, true
}
