-- File này chứa câu lệnh sqlc do User module sở hữu để tạo profile tối thiểu trong registration transaction của AUTH-001.

-- name: CreateRegistrationUser :exec
INSERT INTO users (
    id,
    display_name
) VALUES (
    $1,
    $2
);
