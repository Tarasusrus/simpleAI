package main

import (
	"errors"

	"simpleAI/internal/telegram"
)

// chatAccess — единственное место, где решается, кого пускает бот: список чатов, либо явное
// ALLOW_ALL=true при пустом списке. Пустой список без подтверждения — ошибка старта.
func chatAccess(allowed []int64, allowAll bool) (telegram.Middleware, error) {
	switch {
	case len(allowed) > 0:
		return telegram.RequireAllowedChats(allowed), nil
	case allowAll:
		return telegram.AllowAnyChat(), nil
	default:
		return nil, errors.New("TELEGRAM_ALLOWED_CHATS пуст: бот открыт всем. Задай список чатов или явно ALLOW_ALL=true")
	}
}
