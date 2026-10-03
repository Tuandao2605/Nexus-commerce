-- File DOWN này rollback bảng refresh_tokens trước khi bất kỳ migration phụ thuộc nào được gỡ.
BEGIN;

DROP TABLE refresh_tokens;

COMMIT;
