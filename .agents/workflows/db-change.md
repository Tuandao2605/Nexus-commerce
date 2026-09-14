# Database Change Workflow

Use this checklist in addition to `ticket.md` for PostgreSQL schema or migration tickets.

1. Inspect the applicable DATA/ERD sections, current schema dependency, latest migrations, and nearby integration tests.
2. Separate database-enforced invariants from repository/service/orchestration rules.
3. Create a new numbered UP/DOWN migration; never rewrite shared applied history.
4. Add focused PostgreSQL tests for constraints, tenant safety, rollback, and concurrency when relevant.
5. Run the focused database test with `make test-integration-target DB_TEST_NAME=<test-or-regex>`.
6. Run `make migrate-integration-cycle` and `make db-migration-status`.
7. Run `make verify-full` when PostgreSQL is available; otherwise report it as `NOT RUN` with the reason.
8. Inspect the migration diff, dependency-safe rollback order, data impact, indexes, and final dirty state.

Use repository targets for Docker and database operations. Direct container access is only for a missing one-off diagnostic and must be non-interactive and time-bounded.
