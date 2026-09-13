-- File DOWN này rollback toàn bộ schema Identity theo thứ tự bảng con trước, bảng users gốc sau.
BEGIN;

DROP TABLE user_addresses;
DROP TABLE sessions;
DROP TABLE credentials;
DROP TABLE users;

COMMIT;
