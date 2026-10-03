-- File này khai báo các thao tác sqlc tối thiểu để Auth quản lý digest và trạng thái refresh token.

-- GetRefreshTokenByHash tìm token theo SHA-256 digest; caller phải tự kiểm tra session/user lifecycle theo use case.
-- name: GetRefreshTokenByHash :one
SELECT id, session_id, token_hash, previous_token_id, created_at, expires_at, consumed_at, revoked_at
FROM refresh_tokens
WHERE token_hash = $1;

-- CreateRefreshToken chỉ thêm token khi session còn hiệu lực và token không vượt expiry của session.
-- name: CreateRefreshToken :execrows
INSERT INTO refresh_tokens (
    id, session_id, token_hash, previous_token_id, created_at, expires_at
)
SELECT sqlc.arg(id), session.id, sqlc.arg(token_hash), sqlc.narg(previous_token_id),
       sqlc.arg(created_at), sqlc.arg(expires_at)
FROM sessions AS session
WHERE session.id = sqlc.arg(session_id)
  AND session.revoked_at IS NULL
  AND session.expires_at > now()
  AND sqlc.arg(expires_at) <= session.expires_at;

-- ConsumeRefreshToken chỉ consume token còn hạn/chưa dùng thuộc session chưa revoke/hết hạn; affected rows báo kết quả CAS.
-- name: ConsumeRefreshToken :execrows
UPDATE refresh_tokens AS token
SET consumed_at = now()
WHERE token.id = $1
  AND token.consumed_at IS NULL
  AND token.revoked_at IS NULL
  AND token.expires_at > now()
  AND EXISTS (
      SELECT 1
      FROM sessions AS session
      WHERE session.id = token.session_id
        AND session.revoked_at IS NULL
        AND session.expires_at > now()
  );

-- RevokeSessionRefreshTokens thu hồi mọi refresh token chưa bị revoke trong session khi logout/revoke session.
-- name: RevokeSessionRefreshTokens :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE session_id = $1
  AND revoked_at IS NULL;
