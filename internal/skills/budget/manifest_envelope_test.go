package budgetskill

import (
	"encoding/json"
	"strings"
	"testing"
)

// ADR-017: LLM больше не видит start_envelope ни в описании, ни в схеме скилла (сам код действия живёт до ovvt.9).
//
// Мутация: вернуть start_envelope в Manifest budget — тест краснеет.
func TestManifest_NoStartEnvelope(t *testing.T) {
	m := NewBudgetSkill(nil).Manifest()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "start_envelope") {
		t.Error("в Manifest budget остался start_envelope")
	}
	if !strings.Contains(string(raw), "set_share_limit") {
		t.Error("set_share_limit должен остаться в Manifest")
	}
}
