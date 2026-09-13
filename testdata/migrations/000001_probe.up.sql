-- File UP này tạo bảng probe tạm để xác nhận golang-migrate có thể apply migration thành công.
CREATE TABLE migration_probe (
    id BIGINT PRIMARY KEY
);
