-- File này cho User module kiểm tra account status trong transaction phát credential của Auth.

-- LockAuthUserStatus ngăn account lifecycle thay đổi trước khi token transaction commit.
-- name: LockAuthUserStatus :one
SELECT status FROM users WHERE id = $1 FOR SHARE;
