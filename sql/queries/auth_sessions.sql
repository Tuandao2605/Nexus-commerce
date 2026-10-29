-- File này cung cấp persistence cho session và rotation dưới session row lock của AUTH-006.

-- CreateAuthSession tạo một session với deadline tuyệt đối từ database clock.
-- name: CreateAuthSession :one
WITH instant AS (SELECT clock_timestamp() AS value)
INSERT INTO sessions (id, user_id, created_at, last_activity_at, expires_at)
SELECT sqlc.arg(id), sqlc.arg(user_id), value, value, value + sqlc.arg(lifetime)::interval
FROM instant
RETURNING *;

-- LockAuthSession serialize rotation/revocation cùng session; caller phải lock session trước token.
-- name: LockAuthSession :one
SELECT * FROM sessions WHERE id = $1 FOR UPDATE;

-- LockAuthRefreshToken đọc lại trạng thái sau session lock, không bỏ lịch sử consumed/expired.
-- name: LockAuthRefreshToken :one
SELECT * FROM refresh_tokens WHERE token_hash = $1 FOR UPDATE;

-- AuthDatabaseTime lấy thời gian thực sau lock wait, không dùng transaction-start now().
-- name: AuthDatabaseTime :one
SELECT clock_timestamp()::timestamptz AS current_time;

-- TouchAuthSession cập nhật activity chỉ khi session vẫn còn hiệu lực tại thời điểm ghi.
-- name: TouchAuthSession :execrows
UPDATE sessions SET last_activity_at = clock_timestamp()
WHERE id = $1 AND revoked_at IS NULL AND expires_at > clock_timestamp();

-- RevokeAuthSession thu hồi session dưới lock mà không gia hạn hay hồi sinh session.
-- name: RevokeAuthSession :execrows
UPDATE sessions SET revoked_at = clock_timestamp()
WHERE id = $1 AND revoked_at IS NULL;
