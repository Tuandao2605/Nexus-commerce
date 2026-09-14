# Nexus-Commerce Agent Rules

## Core

- Inspect relevant code, tests, docs, and current Git state before modifying files.
- Follow implemented architecture and conventions; distinguish them from planned work.
- Make the smallest coherent ticket change and avoid unrelated refactors.
- Preserve unrelated user changes in a dirty worktree.
- Never claim a check passed unless it was actually run successfully.

## Minimum Necessary Context

- Start with this file and the ticket; do not front-load the whole repository.
- Read `.agents/workflows/ticket.md` for normal ticket execution.
- Read `.agents/workflows/db-change.md` only for schema or migration work.
- Load only skills whose descriptions match the task.
- Inspect only relevant source, tests, configuration, migrations, and design sections.
- Expand logs, files, references, and diagnostics only after the task or a failure requires them.

## Architecture and Implementation

- Respect module ownership and existing dependency direction.
- Keep handlers thin, business rules in services/use cases, and persistence in repositories.
- Prefer constructor dependency injection; avoid global mutable state and speculative abstractions.
- Add or update the narrowest tests that prove changed behavior.
- Do not delete, skip, or weaken valid tests to obtain a pass.
- Add comments only where they clarify non-obvious intent, invariants, exported APIs, or test purpose; avoid comments that merely restate the code.

## Security and Safety

- Validate external input and enforce authentication, authorization, ownership, and Shop scope server-side.
- Never expose or log passwords, tokens, secrets, credentials, or sensitive payment data.
- Never concatenate untrusted input into SQL.
- Inspect exact targets before destructive actions; do not use destructive commands as debugging shortcuts.
- Never destroy persistent Docker volumes or databases without explicit user authorization.
- Keep secrets and local environment files out of commits.

## Execution Interface

- Prefer Makefile targets over reconstructed raw commands.
- Run `make agent-preflight` before code or environment-dependent implementation work.
- Use `make test-target` during focused iteration, `make verify-fast` before normal completion,
  and `make verify-full` only when database/integration confidence is relevant.
- Use `make db-migration-status` for test migration state.
- If a reusable operation is missing, add one narrow safe target rather than repeating raw commands.
- Direct container commands are an escape hatch only: non-interactive, stdin-disabled, and time-bounded.

## Verification and Scope

- Prefer targeted verification first, then broaden in proportion to risk.
- Database changes require a new migration, rollback review, integration tests, and final non-dirty status.
- Review `git status`, diff statistics, changed paths, and relevant diff content before completion.
- Check concurrency, idempotency, transaction, historical-data, and security risks only when relevant.
- Keep successful output concise; expand diagnostics on failure.

## Skills

- `.agents/skills/` contains specialized implementation knowledge, not global policy.
- `nexus-ecommerce` owns Nexus domain boundaries and repository-specific reliability invariants.
- Use backend, architecture, database, testing, security, or frontend skills selectively.
- Skills and workflows do not override this file or the user's explicit request.

## Completion Report

- Report what was implemented and which files changed.
- Report exact verification as `PASS`, `FAIL`, or `NOT RUN` with a short reason.
- Mention material risks, limitations, or deferred scope without pasting routine command transcripts.
