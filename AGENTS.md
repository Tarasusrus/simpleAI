# Agent Instructions

This project uses **bd** (beads) for issue tracking. Run `bd onboard` to get started.

## Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --status in_progress  # Claim work
bd close <id>         # Complete work
bd sync               # Sync with git
```

## Landing the Plane (Session Completion)

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   bd sync
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
- ALWAYS start with bd show <id> for task context.
- DO NOT scan the repository unless:
  •	issue explicitly requires it
  •	bd context is insufficient
  •	Treat bd issues as the source of truth.
- Каждый эпик выполняется в своей ветке 
- помимо отражений изменений в файле [TECHNICAL_README.md](TECHNICAL_README.md) обязательно дополняй ридми простым человеческим языком рассказываю что сейчас происходит какой функционал есть как этим пользоваться какой флоу и тд чтобы я не вдавай в технические подробности мог понять на каком сейчас этапе развитеие проекта
- каждый файл в проекте должен содержать докстринг с описание на русском для чего он нужен и какую задачу он выполняет
