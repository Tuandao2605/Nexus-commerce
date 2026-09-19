-- File này chứa câu lệnh sqlc do Auth sở hữu để ghi Credential trong registration transaction của AUTH-001.

-- name: CreateRegistrationCredential :exec
INSERT INTO credentials (
    user_id,
    email,
    password_hash
) VALUES (
    $1,
    $2,
    $3
);
