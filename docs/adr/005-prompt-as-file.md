# ADR-005: Prompt-as-File Convention

**Date:** 2026-05-09  
**Status:** Accepted  
**Issues:** simpleAI-5mi

---

## Context

LLM prompt templates were inlined as Go string constants inside skill files (`advisorPromptTemplate`, `analyzePromptTemplate`). This had two problems:

1. Editing a prompt required recompiling the binary — slow iteration during eval/tuning.
2. Prompts mixed with Go code made diffs noisy and template content hard to read.

---

## Decision

All multi-line LLM prompt templates move to `internal/prompts/<skill>/<name>.tmpl` files.

### Structure

```
internal/prompts/
├── advisor/
│   ├── advice.tmpl
│   └── analyze.tmpl
└── prompts.go          # //go:embed all:advisor; func Get(name string) string
```

### API

```go
import "simpleAI/internal/prompts"

tmpl := prompts.Get("advisor/advice.tmpl")   // returns string, panics if missing
prompt := fmt.Sprintf(tmpl, arg1, arg2, ...)
```

### Dev hot-reload

Set `PROMPTS_DIR=/path/to/internal/prompts` to load from disk without recompile. Useful for iterative A/B testing in evals. Production always uses embedded files.

### Template format

Files use `fmt.Sprintf` placeholders (`%s`, `%d`, `%.0f`). Argument order must match the call site. Changes to argument count are compile-time detectable via vet.

---

## Consequences

- Prompts editable without recompile when `PROMPTS_DIR` is set.
- Binary still self-contained in production (go:embed).
- Missing template = panic at startup — intentional, fail fast.
- New skills must place prompts in `internal/prompts/<skill>/` not inline in Go code.
