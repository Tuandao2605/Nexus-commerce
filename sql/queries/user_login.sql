-- File này chứa câu lệnh sqlc do User module sở hữu để Auth kiểm tra account lifecycle sau khi password đúng.

-- name: FindLoginUserStatus :one
SELECT status
FROM users
WHERE id = $1;
