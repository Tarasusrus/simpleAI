package advisorskill

import "testing"

// Результат навыка детерминирован и готов к показу: агент отдаёт его как есть, без пересказа LLM.
func TestManifestDirectReply(t *testing.T) {
	if !(&AdvisorSkill{}).Manifest().DirectReply {
		t.Fatal("навык должен быть DirectReply: иначе LLM пересказывает его ответ")
	}
}
