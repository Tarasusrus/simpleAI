package skills

import "testing"

// rag_search возвращает промпт с фрагментами, а не готовый ответ: ему нужен синтез LLM.
func TestRAGSearchIsNotDirectReply(t *testing.T) {
	if (&RAGSearchSkill{}).Manifest().DirectReply {
		t.Fatal("rag_search не должен быть DirectReply: пользователь получил бы сырой промпт")
	}
}
