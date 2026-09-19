-- File này chứa các câu lệnh sqlc do Auth sở hữu để lookup Credential và opportunistic rehash trong AUTH-003.

-- name: FindLoginCredentialByEmail :one
SELECT
    user_id::text AS user_id,
    email,
    password_hash
FROM credentials
WHERE email = $1;

-- name: UpgradeLoginPasswordHash :execrows
UPDATE credentials
SET
    password_hash = sqlc.arg(new_password_hash),
    updated_at = now()
WHERE user_id = sqlc.arg(user_id)
  AND password_hash = sqlc.arg(old_password_hash);
