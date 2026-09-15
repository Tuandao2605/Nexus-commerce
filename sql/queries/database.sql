-- File này chứa query kỹ thuật tối thiểu để DB-005 kiểm chứng sqlc codegen, pgx/v5, context và transaction wiring.

-- name: DatabasePing :one
SELECT 1::bigint AS value;
