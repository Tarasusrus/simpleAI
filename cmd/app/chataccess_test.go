package main

import (
	"testing"
)

func TestChatAccess(t *testing.T) {
	if _, err := chatAccess(nil, false); err == nil {
		t.Fatal("пустой список без ALLOW_ALL должен быть ошибкой")
	}
	if mw, err := chatAccess(nil, true); err != nil || mw == nil {
		t.Fatalf("пустой список + ALLOW_ALL: %v", err)
	}
	if mw, err := chatAccess([]int64{1}, false); err != nil || mw == nil {
		t.Fatalf("непустой список: %v", err)
	}
}
