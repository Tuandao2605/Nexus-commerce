-- File này tạo database riêng cho domain integration tests và migration-engine probe khi volume khởi tạo lần đầu.
CREATE DATABASE nexus_commerce_test OWNER nexus;
CREATE DATABASE nexus_commerce_migration_probe OWNER nexus;
