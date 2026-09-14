-- File DOWN này rollback schema Cart theo thứ tự CartItem phụ thuộc trước rồi Cart gốc sau.
BEGIN;

DROP TABLE cart_items;
DROP TABLE carts;

COMMIT;
