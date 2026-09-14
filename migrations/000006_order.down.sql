-- File DOWN này rollback schema Order theo thứ tự status history, OrderItem rồi Parent/Seller Order.
BEGIN;

DROP TABLE order_status_histories;
DROP TABLE order_items;
DROP TABLE orders;

COMMIT;
