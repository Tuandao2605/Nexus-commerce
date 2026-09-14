-- File DOWN này rollback schema Catalog theo thứ tự SKU, Variant, Product rồi các bảng phân loại gốc.
BEGIN;

DROP TABLE skus;
DROP TABLE product_variants;
DROP TABLE products;
DROP TABLE brands;
DROP TABLE categories;

COMMIT;
