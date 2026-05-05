# simpleAI — Open Source MCP Gateway

> **Multi-model MCP gateway for personal AI agents.**  
> Route tasks across DeepSeek, Gemini, and Claude via standard Model Context Protocol.  
> Built in Go. Self-hosted. Production-ready.

[![MCP](https://img.shields.io/badge/MCP-SSE%20%3A8080-7c6af7)](https://modelcontextprotocol.io)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8)](https://go.dev)
[![License](https://img.shields.io/badge/License-MIT-green)](LICENSE)

**[mcpgate.org](https://mcpgate.org)** · [Quick Start](#setup) · [Adding Skills](#adding-a-new-skill)

---

## What is this?

An open-source MCP (Model Context Protocol) gateway that exposes personal AI agent tools to any MCP-compatible client (Claude Code, etc.).

**Multi-model routing:**
- **DeepSeek** — commodity tasks (budget tracking, search)
- **Gemini** — fallback
- **Claude** — agentic planning, reasoning-heavy workflows, MCP tool calls

Connect once via SSE, get access to all skills:

```json
{
  "mcpServers": {
    "simpleai": { "type": "http", "url": "http://your-host:8080/sse" }
  }
}
```

---

## Architecture

```
User
 │
 ├─── Telegram message
 │        ↓
 │    Telegram Bot (cmd/telegram)
 │        ↓
 │    agent.Service  ←── plugin.Registry ──┬── RAGSearchSkill
 │        ↓                                ├── BudgetSkill
 │    LLM (DeepSeek primary, Gemini fallback, Claude for agentic)
 │
 └─── Claude Code / external agent
          ↓ HTTP/SSE :8080
      MCP Server (cmd/mcp)
          ↓
      plugin.Registry (shared)
```

**Key design:** `plugin.Registry` is the single source of truth for available tools.  
Both entry points (Telegram and MCP) use the same registry — add a skill once, available everywhere.

---

## Features

- **MCP server** — HTTP/SSE gateway on `:8080` for Claude Code and other MCP clients
- **Multi-model routing** — DeepSeek → Gemini → Claude pipeline
- **Budget tracker** — expenses, income, goals, debts, multi-currency
- **Expense forecasting** — category-level forecast with trend analysis
- **Recurring payments** — auto-transactions on schedule with notifications
- **RAG search** — knowledge base search over documents via pgvector
- **Mail digest** — Gmail/IMAP integration
- **Daily reminders** — configurable by time and timezone
- **CI/CD** — GitHub Actions → SSH → Docker Compose → systemd (~2 min deploy)

---

## Project Structure

```
cmd/
  app/          — Main entry: runs Telegram bot + MCP server + workers
  agent/        — CLI agent: reads stdin, responds via LLM (e.g. git diff → code review)
  embeddings/   — Generate embeddings for rag_document records
  ingest/       — Load receipts from JSON into DB + rag_document
  mcp/          — MCP HTTP/SSE server on :8080
  rag-query/    — CLI for manual RAG search testing
  telegram/     — Telegram bot (standalone)
  worker/       — Background worker

internal/
  agent/        — agent.Service: tool calling loop for Telegram
  plugin/       — Registry, Skill interface, Manifest
  skills/       — RAGSearchSkill, BudgetSkill (add more here)
  rag/          — pgvector store, retriever, prompt builder
  budget/       — Budget models and CRUD store
  mail/         — Gmail/IMAP integration
  mcp/          — plugin.Registry → mcp-go adapter
  notify/       — Telegram notifications from background tasks
  adapters/     — LLM factory (OpenAI, Ollama, auto-fallback), Telegram wrapper
```

---

## How It Works

### Tool calling loop (Telegram)
1. Build system prompt with available skills list
2. Send to LLM → get JSON `{"skill": "...", "input": {...}}`
3. Execute skill, pass result back to LLM
4. Return final answer to user

### RAG search example
```
User: "find receipts from January"
  → LLM → {"skill":"rag_search","input":{"query":"receipts January"}}
  → embed query → pgvector search → BuildPrompt → LLM → answer
```

### Budget tracker example
```
User: "spent 1500 on groceries"
  → LLM → {"skill":"budget","input":{"action":"add_expense","amount":1500,"category":"food"}}
  → BudgetSkill → AddTransaction → GetSummary → answer
```

---

## Adding a New Skill

1. Create `internal/skills/my_skill.go`, implement `plugin.Skill`:
   ```go
   func (s *MySkill) Manifest() plugin.Manifest { ... }
   func (s *MySkill) Run(ctx context.Context, input string) (string, error) { ... }
   ```
2. Register in `cmd/app/main.go` → `buildRegistry()`.
3. Update README.

See `rag_search.go` (simple) and `budget_skill.go` (action-based) as examples.

---

## Setup

### Prerequisites
- Go 1.24+
- Docker (for PostgreSQL + pgvector)

### Run

```bash
cp .env.example .env   # fill in required values
docker compose up -d   # start Postgres
make migrate-up        # apply migrations
make run-all           # start everything
```

`make run-all` starts in one process:
- Telegram bot
- MCP SSE server on `:8080`
- Mail worker (if `MAIL_ACCOUNTS_JSON` is set)

### MCP server (for Claude Code)

```bash
make run-mcp
```

`.mcp.json` is already in the root:
```json
{
  "mcpServers": {
    "simpleai": { "type": "http", "url": "http://localhost:8080/sse" }
  }
}
```

Verify in Claude Code: `/mcp` → should show `rag_search` tool.

### Code review via CLI

```bash
git diff | go run cmd/agent/main.go
# or:
make run-diff
```

---

## Environment Variables

```env
# LLM
LLM_PROVIDER=openai                # openai | ollama
LLM_CHAT_MODEL=gpt-4.1-mini
EMBEDDING_MODEL=text-embedding-3-small
API_KEY=sk-...

# Ollama (if LLM_PROVIDER=ollama)
OLLAMA_BASE_URL=http://localhost:11434
OLLAMA_MODEL=qwen2.5:7b-instruct-q4_K_M
OLLAMA_EMBED_MODEL=nomic-embed-text

# Database
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_DB=simpleai
POSTGRES_USER=simpleai
POSTGRES_PASSWORD=simpleai

# Telegram
TELEGRAM_BOT_TOKEN=...
TELEGRAM_ALLOWED_CHATS=123456789
TELEGRAM_WORKERS=4

# System
SYS_PROMPT=You are a helpful assistant...
LOG_LEVEL=info        # debug | info | warn | error
LOG_FORMAT=text       # text | json

# Observability (Langfuse, optional — leave empty to disable)
LANGFUSE_HOST=http://localhost:3001
LANGFUSE_PUBLIC_KEY=pk-lf-rag-mm-dr-local
LANGFUSE_SECRET_KEY=sk-lf-...
```

---

## Observability (Langfuse)

Self-hosted Langfuse поднимается отдельным compose в `deploy/langfuse/`. После запуска агент шлёт трейсы (trace на запрос → generation на каждый LLM-вызов → span на каждый tool call) в http://localhost:3001/project/rag-mm/traces.

Подробности: [deploy/langfuse/README.md](deploy/langfuse/README.md).

Если переменные `LANGFUSE_*` не заданы — трейсинг просто отключается, агент работает как раньше.

---

## Make Targets

| Command | Description |
|---------|-------------|
| `make run-all` | **Start everything** — Telegram + MCP + worker |
| `make db-up` | Start Postgres in Docker |
| `make migrate-up` | Apply SQL migrations |
| `make migrate-down` | Rollback last migration |
| `make run-mcp` | MCP SSE server only on :8080 |
| `make run-diff` | Git diff → code review agent |
| `make lint` | Run golangci-lint |

---

## Stack

Go · PostgreSQL · pgvector · Docker · GitHub Actions · DeepSeek · Gemini · Claude (Bedrock) · goose · slog
