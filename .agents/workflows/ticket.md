# Ticket Workflow

Use this checklist for a normal repository ticket. Read `db-change.md` as well only when schema or migration work is involved.

1. Read the ticket; run `make agent-preflight` before code or environment-dependent work.
2. Inspect current Git state plus only the relevant source, tests, configuration, and docs.
3. Classify the task and load only matching skills:
   - commerce backend/API: `nexus-ecommerce` plus `senior-backend`;
   - database/migration: also use `db-change.md` and relevant database references;
   - architecture decision: `senior-architect`, plus `nexus-ecommerce` for Nexus boundaries;
   - frontend: only the checked-in framework/library skills actually touched.
4. Identify ownership, behavior contract, failure paths, and the smallest coherent change.
5. Implement without unrelated cleanup; add/update focused tests.
6. Iterate with `make test-target TEST_PACKAGE=<package> TEST_NAME=<test-or-regex>`.
7. Run `make verify-fast`; run `make verify-full` only when integration/database coverage is relevant.
8. Run `make diff-check`, inspect the relevant diff content, and review proportional risks.
9. Report implementation, changed files, exact verification, and remaining limitations.

On failure, inspect the focused error first and expand context only as needed. Do not switch to verbose global logs by default.
