# AGENTS.md

Project-wide rules for `lark-acp-bridge`. Every contributor and AI agent
working in this repository MUST follow these rules.

## Language

**All content produced INTO this project MUST be in English.** This rule
governs artifacts that live in the repository, not the conversation between
the user and the agent. Specifically, it covers:

- Source code identifiers (variables, functions, types, packages)
- Code comments
- Commit messages
- Pull request titles and bodies
- Documentation files (`README.md`, `DESIGN.md`, etc.)
- User-facing strings emitted by the bot (slash-command replies, card text,
  log messages, error messages) — in their canonical form. English literals
  are the message keys and defaults; localized output is provided only
  through the `internal/i18n` package (see the exception below).
- This `AGENTS.md` file and any future agent-instruction files

Do NOT mix Chinese (or any other non-English language) into code, docs,
commits, or bot output. If you copy a design idea from a Chinese-language
upstream project (e.g. `lark-coding-agent-bridge`), translate the concept
to English when writing it down here.

This rule does NOT constrain the language of conversational replies from
the agent to the user (e.g. chat responses in the terminal). The reply
language is chosen by the user and is out of scope for this file.

Exceptions are limited to:
- Quoting external source material verbatim (clearly marked as a quote).
- Proper nouns, product names, or API field names that are themselves
  non-English by definition.
- Localization data in the `internal/i18n` package: translation tables
  (e.g. `zh.go`) and the Chinese slash-command aliases in
  `internal/commands`. Every localized string must have an English
  canonical key; the bot's default output language remains English
  (config `language: en`).
- The `/new-issue` prompt template and `README.zh.md`, which are
  Chinese-facing product surfaces by design.

## Project intent

`lark-acp-bridge` is a standalone Go project that bridges Feishu / Lark
messenger with local CLI coding agents. It is written from scratch in Go
and does not share code with the TypeScript project
`lark-coding-agent-bridge`, although it borrows design ideas from it.

v1 scope: **Devin only**, driven through the Agent Client Protocol (ACP)
via `devin acp` (JSON-RPC over stdio). Claude is deferred to v2.

v1.1 scope: **Codex** adapter (`codex exec --json` NDJSON) and the
`/provider` switch command, which lets each chat override the default
provider per-scope. Switching provider clears the current session.

## Tech stack

- Go 1.26+
- `github.com/larksuite/oapi-sdk-go/v3` (Channel module) for Feishu/Lark
  transport, message normalization, streaming replies, and interactive
  cards.
- `os/exec` + `bufio` + `encoding/json` for the ACP stdio client. No
  external ACP SDK dependency in v1.
- Standard library only for everything else unless a clear need arises.

## Architecture constraints

- The `AgentAdapter` interface is the seam between the Feishu side and the
  agent side. v1.1 ships `DevinAdapter` and `CodexAdapter`; v2 will add
  `ClaudeAdapter` without changing the interface.
- Slash commands handled locally by the bridge (e.g. `/new`, `/cd`, `/ws`,
  `/model`, `/help`) MUST NOT invoke the agent subprocess and MUST NOT
  consume tokens. Only plain user messages are forwarded to the agent.
- `/help` is dynamic: it shows bridge commands always, and agent-specific
  commands only when a provider is selected.
- Switching provider or model clears the current session (context loss is
  acceptable, per the product spec).
- Sessions are scoped per chat (`chatId`) or per topic
  (`chatId:threadId`), never global.

## Coding style

- Follow effective Go and the standard `gofmt` / `go vet` output.
- Compact code: collapse duplicate branches, avoid unnecessary nesting,
  share abstractions.
- Handle errors at the right boundary, not on every line. Prefer returning
  errors up to a meaningful handler over wrapping every call in a
  try/catch-style block.
- Do NOT add or remove comments unless asked. Preserve existing comments
  when editing.
- Emojis are allowed in bot-facing output (card text, status indicators,
  slash-command replies) where they improve readability. Avoid emojis in
  code identifiers, commit messages, and internal log messages.
- Do not create documentation files proactively; only `AGENTS.md`,
  `DESIGN.md`, and `README.md` are expected.

## Verification

Before considering a task complete, run:

```bash
go build ./...
go vet ./...
go test ./...
```

Integration tests that spawn a real `devin acp` subprocess are gated behind
the `integration` build tag. Run them only when devin is installed and
logged in:

```bash
go test -tags integration ./internal/agent/devin/acp/ -v -timeout 180s
```

If a verification step has no implementation yet (e.g. no tests), note it
explicitly rather than skipping silently.

## Git

- Never update git config.
- Never use `-i` flags.
- Do not push unless explicitly asked.
- Do not commit if there are no changes.
- Commit messages in English, focused on why not what.
