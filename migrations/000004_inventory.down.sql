-- File DOWN này rollback schema Inventory theo thứ tự ledger/items phụ thuộc trước, Warehouse gốc sau.
BEGIN;

DROP TABLE stock_movements;
DROP TABLE inventory_reservation_items;
DROP TABLE inventory_reservations;
DROP TABLE inventory_stocks;
DROP TABLE warehouses;

COMMIT;
