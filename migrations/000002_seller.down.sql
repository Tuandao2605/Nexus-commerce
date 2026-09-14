-- File DOWN này rollback schema Seller theo thứ tự membership phụ thuộc trước, SellerAccount gốc sau.
BEGIN;

DROP TABLE shop_memberships;
DROP TABLE shops;
DROP TABLE seller_accounts;

COMMIT;