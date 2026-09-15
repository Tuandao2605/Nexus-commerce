-- File DOWN này rollback schema Voucher V1 theo thứ tự voucher_usages phụ thuộc trước rồi vouchers sau.
BEGIN;

DROP TABLE voucher_usages;
DROP TABLE vouchers;

COMMIT;

