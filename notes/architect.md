===============================================================================
                       SYSTEM ARCHITECTURE DOCUMENTATION (V2)
                             PROJECT: NEXUS-COMMERCE
                             TICKET: ECOM-ARCH-002
===============================================================================

1. OVERVIEW & DESIGN PRINCIPLES
-------------------------------------------------------------------------------
Nexus-Commerce được thiết kế theo kiến trúc Modular Monolith nhằm đảm bảo sự
phân tách ranh giới rõ ràng (Loose Coupling, High Cohesion), sẵn sàng cho việc
tách thành Microservices trong tương lai.

Các nguyên tắc cốt lõi:
- Single Data Ownership: Mỗi database table/entity chỉ do đúng 1 Module sở hữu.
- Single Responsibility Principle (SRP): Mỗi module chỉ có một lý do duy nhất để thay đổi.
- Acyclic Dependency Graph: Không cho phép phụ thuộc vòng (Circular Dependency/Import Cycle).
- Boundary-Driven Communication: Giao tiếp qua Application Interface hoặc Domain Events.
- Immutability via Snapshot: Dữ liệu lịch sử giao dịch được bảo toàn độc lập, có thể
  tái tạo hoàn toàn (reconstructable) mà không cần truy vấn dữ liệu live ở Catalog/User/Seller.


2. THE 10 CORE MODULES & DATA OWNERSHIP MATRIX
-------------------------------------------------------------------------------
Hệ thống bao gồm đúng 10 modules độc lập theo yêu cầu ticket:

1. Auth Module:
   - Scope: Quản lý thông tin xác thực, phiên làm việc và bảo mật.
   - Entities Owned: credentials, password_hashes, sessions, refresh_tokens.

2. User Module:
   - Scope: Quản lý hồ sơ cá nhân và thông tin người dùng cuối (Buyer).
   - Entities Owned: user_profiles, user_addresses, user_preferences.

3. Seller Module:
   - Scope: Quản lý tài khoản người bán, thông tin cửa hàng và quyền sở hữu shop.
   - Entities Owned: seller_accounts, shops, shop_memberships, seller_statuses.

4. Catalog Module:
   - Scope: Quản lý danh mục, thông tin sản phẩm và thuộc tính biến thể (SKU).
     (Lưu ý: Shop KHÔNG do Catalog sở hữu mà do Seller module sở hữu).
   - Entities Owned: categories, products, skus, product_attributes.

5. Inventory Module:
   - Scope: Quản lý số lượng tồn kho thực tế, tồn kho khả dụng (Available Stock),
     và giữ chỗ kho tạm thời (Reservations with TTL).
   - Entities Owned: inventory_stocks, inventory_reservations.

6. Cart Module:
   - Scope: Lưu trữ các sản phẩm tạm thời trong giỏ hàng của người dùng.
   - Entities Owned: carts, cart_items.

7. Order Module:
   - Scope: Quản lý đơn hàng, chi tiết đơn hàng, lịch sử trạng thái đơn hàng.
     Đồng thời chứa CheckoutService (Application Orchestrator) điều phối quy trình checkout.
   - Entities Owned: orders, order_items, order_status_histories.

8. Payment Module:
   - Scope: Kết nối cổng thanh toán (VNPay, Momo), quản lý giao dịch thanh toán.
   - Entities Owned: payment_transactions, payment_refunds.

9. Voucher Module:
   - Scope: Quản lý mã giảm giá, kiểm tra điều kiện áp dụng và lượt sử dụng.
   - Entities Owned: vouchers, voucher_usages, voucher_conditions.

10. Notification Module:
    - Scope: Quản lý việc gửi và lịch sử thông báo (Email, Push Notification, SMS).
    - Entities Owned: notification_records, notification_templates, delivery_logs.


3. LAYERED ARCHITECTURE & ARCHITECTURAL BOUNDARIES
-------------------------------------------------------------------------------
Hệ thống phân chia tri thức theo các tầng (từ dưới lên trên):

+-----------------------------------------------------------------------------+
| TẦNG 3: ORCHESTRATION & APPLICATION USE CASES                                |
| - Modules: Order (CheckoutService), Cart                                    |
| - Tương tác: Điều phối quy trình qua nhiều Domain Modules                   |
+-----------------------------------------------------------------------------+
                                      ^
                                      |
+-----------------------------------------------------------------------------+
| TẦNG 2: DOMAIN CORE & BUSINESS SERVICES                                     |
| - Modules: Catalog, Inventory, Voucher, Payment, Seller                      |
| - Tương tác: Chứa quy tắc nghiệp vụ cốt lõi                                 |
+-----------------------------------------------------------------------------+
                                      ^
                                      |
+-----------------------------------------------------------------------------+
| TẦNG 1: FOUNDATION & IDENTITY                                               |
| - Modules: Auth, User, Notification                                         |
| - Tương tác: Cung cấp nền tảng danh tính và tiện ích dùng chung             |
+-----------------------------------------------------------------------------+

LƯU Ý VỀ TÁCH BIỆT DOMAIN VS APPLICATION IN ORDER MODULE:
- Entity Order (Domain Entity): Chỉ chứa business rules của bản thân đơn hàng,
  quản lý trạng thái hợp lệ (State Transitions). KHÔNG trực tiếp gọi các API/Infrastructure
  của Inventory, Voucher hay Payment.
- CheckoutService (Application Service / Orchestrator): Nằm trong Order Module nhưng
  ở tầng Application Layer, chịu trách nhiệm điều phối dòng chảy Checkout (gọi Catalog,
  Voucher, Inventory, Order Repository, Cart, Payment).


4. MODULE DEPENDENCY GRAPH
-------------------------------------------------------------------------------
Sơ đồ phụ thuộc giữa 10 Modules (Đảm bảo không có Circular Dependency):

                 +----------+          +----------+
                 |   Auth   | -------->|   User   |
                 +----------+          +----------+
                                            ^
                                            | (Sync API)
                 +----------+               |
                 |  Seller  | --------------+
                 +----------+
                      ^
                      | (Sync API: Shop Validation)
                      |
                 +----------+          +----------+
                 | Catalog  | <------- |   Cart   |
                 +----------+ (Sync)   +----------+
                      ^                     ^
                      | (Sync)              | (Sync)
                      +-------+      +------+
                              |      |
                              v      v
                           +------------+
                           |   Order    | <======= (Async Event: OrderPaid / PaymentFailed)
                           +------------+                               ^
                            /    |     \                                |
                   (Sync)  /     |      \ (Sync)                        |
                          v      |       v                        +-----------+
              +-----------+      |     +-----------+              |  Payment  |
              | Inventory |      |     |  Voucher  |              +-----------+
              +-----------+      |     +-----------+                    ^
                                 |                                      |
                                 +--------------------------------------+
                                            (Sync: Init Transaction)

               ---------------------------------------------------
               Notification (Async Event Listener)
                 ^
                 |--- Listen: OrderCreated, PaymentSucceeded, etc.
                 +--- Subscribed to: Order / Payment / Auth

Bản chất giao tiếp (Communication Patterns):
1. Auth -> User (Synchronous Internal Call): Auth xác thực danh tính và lấy User ID.
2. Seller -> User (Synchronous Internal Call): Seller liên kết với User Profile để xác nhận chủ sở hữu.
3. Catalog -> Seller (Synchronous Internal Call): Catalog kiểm tra trạng thái Shop (Active/Inactive) khi đăng sản phẩm.
4. Cart -> Catalog (Synchronous Internal Call): Cart lấy thông tin hiển thị cơ bản của SKU.
5. Order (CheckoutService) -> Cart, Catalog, Voucher, Inventory, Payment (Synchronous Orchestration):
   Checkout process cần phản hồi tức thì để xác nhận đơn hàng thành công hay thất bại.
6. Payment -> Order (Asynchronous Domain Event - `PaymentSucceeded` / `PaymentFailed`):
   Payment KHÔNG gọi trực tiếp Order để update DB. Payment phát ra Event, Order lắng nghe và tự thực hiện State Transition sang PAID.
7. Order / Payment / Auth -> Notification (Asynchronous Domain Events):
   Các module phát event, Notification module lắng nghe và gửi Mail/Push bất đồng bộ.


5. CHECKOUT FLOW & SEQUENCE DIAGRAM
-------------------------------------------------------------------------------
[CheckoutService đóng vai trò Application Orchestrator bên trong Order Module]

Sequence Diagram Flow:

Customer    CheckoutService(Order)    Cart     Catalog    Voucher   Inventory   Order DB    Payment
   |                 |                 |          |          |          |          |           |
   |-- 1. Checkout ->|                 |          |          |          |          |           |
   |                 |-- 2. GetItems ->|          |          |          |          |           |
   |                 |<-- Items -------|          |          |          |          |           |
   |                 |                            |          |          |          |           |
   |                 |-- 3. GetSKUDetails ------->|          |          |          |           |
   |                 |<-- SKU Info & Live Price --|          |          |          |           |
   |                 |   [Check: Price & Active]  |          |          |          |           |
   |                 |                                       |          |          |           |
   |                 |-- 4. Validate & Calculate Discount -->|          |          |           |
   |                 |<-- Discount Amount -------------------|          |          |           |
   |                 |                                                  |          |           |
   |                 |-- 5. ReserveStock(SKU_List, TTL: 15m) ---------->|          |           |
   |                 |    [FAILURE PATH 1: Out of Stock]                |          |           |
   |                 |<-- Reserve Failed -------------------------------|          |           |
   |<-- Return Err --|                                                  |          |           |
   |                 |                                                             |           |
   |                 |-- 6. Save Order & OrderItems Snapshot --------------------->|           |
   |                 |    [FAILURE PATH 2: Order DB Insert Failed / Crash]         |           |
   |                 |    a. Primary Compensation: ReleaseReservation ----------->|           |
   |                 |    b. Safety Net: TTL 15m Expiration Worker                |           |
   |<-- Return Err --|<-- Return Failure ACK --------------------------------------|           |
   |                 |                                                                         |
   |                 |-- 7. ClearSelectedItems -> Cart                                         |
   |                 |-- 8. CreateTransaction ------------------------------------------------>|
   |<-- Order OK ----|<-- Return PaymentURL / QR Code -----------------------------------------|

Chi tiết các bước thực hiện:
- Step 1 (Initiation): Customer chọn sản phẩm và bấm "Đặt hàng". Request chứa `CartItemIDs`, `VoucherID`, `AddressID`.
- Step 2 (Fetch Cart): CheckoutService đọc danh sách `SKU_ID` và `Quantity` từ Cart Module.
- Step 3 (Validate Catalog): CheckoutService gọi Catalog lấy giá niêm yết hiện tại và trạng thái SKU. Nếu đổi giá hoặc ngưng bán -> Hủy Checkout.
- Step 4 (Validate Voucher): CheckoutService gọi Voucher Module kiểm tra điều kiện mã giảm giá. Nếu không hợp lệ -> Hủy Checkout.
- Step 5 & Failure Path 1 (Reserve Inventory): CheckoutService gọi Inventory để giữ chỗ tồn kho với TTL = 15 phút.
  + Failure Path 1: Nếu tồn kho khả dụng không đủ -> Dừng ngay lập tức, báo lỗi hết hàng. Không ghi DB Order.
- Step 6 & Failure Path 2 (Create Order & Compensation):
  CheckoutService tạo record Order (PENDING_PAYMENT) kèm dữ liệu Snapshot.
  + Failure Path 2 (Cơ chế đền bù kép):
    1. Primary Compensation (Trực tiếp): Nếu ghi DB Order thất bại (Validation error, DB Deadlock...),
       CheckoutService gọi ngay `Inventory.ReleaseReservation(ReservationID)`.
    2. Safety Mechanism (Phòng ngừa Crash): Nếu API Process bị crash/sập nguồn ngay sau bước Reserve Inventory
       mà bước Primary Compensation chưa kịp chạy, Worker chạy ngầm của Inventory sẽ tự động quét
       và giải phóng các Reservation đã hết hạn TTL (15 phút).
- Step 7 (Cleanup Cart): Ghi DB Order thành công, CheckoutService xóa các items đã mua khỏi Cart.
- Step 8 (Init Payment): CheckoutService gọi Payment Module sinh giao dịch thanh toán (Momo/VNPay QR) và trả về `PaymentURL` cho Customer.


6. SNAPSHOT DESIGN SPECIFICATION
-------------------------------------------------------------------------------
Nguyên tắc chuẩn xác:
"Dữ liệu lịch sử đơn hàng phải có khả năng tái tạo hoàn toàn (reconstructable entirely)
từ dữ liệu Snapshot của chính nó, mà không đòi hỏi thông tin live từ Catalog hay User Profile."

Điều này KHÔNG đồng nghĩa với việc loại bỏ hoàn toàn Foreign Key / Reference. Các ID gốc
vẫn được lưu trữ để phục vụ mục đích Traceability & Audit Trail.

Cấu trúc lưu trữ chuẩn cho `order_items`:
- `id`: BIGINT / UUID (Primary Key)
- `order_id`: BIGINT / UUID (FK -> orders)
- `sku_id`: BIGINT (FK Reference đến catalog_skus - dùng để trace/audit)
- `product_id`: BIGINT (FK Reference đến catalog_products - dùng để trace)
- `product_name_snapshot`: VARCHAR(255) (Tên sản phẩm tại thời điểm mua)
- `sku_name_snapshot`: VARCHAR(255) (Tên biến thể: Màu, Size tại thời điểm mua)
- `unit_price_snapshot`: DECIMAL(12, 2) (Giá bán thực tế tại thời điểm mua)
- `original_price_snapshot`: DECIMAL(12, 2) (Giá niêm yết tại thời điểm mua)
- `quantity`: INT (Số lượng mua)

Địa chỉ giao hàng trong `orders`:
- `shipping_address_snapshot`: JSONB / Embedded Columns (Tên người nhận, SĐT, Địa chỉ chi tiết)
  được copy từ User Address, đảm bảo nếu User đổi địa chỉ sau này thì đơn cũ không đổi.


7. ARCHITECTURE DECISIONS (8 CRITICAL QUESTIONS ANSWERED)
-------------------------------------------------------------------------------
1. Who owns stock?
   -> Inventory Module sở hữu hoàn toàn tồn kho thực tế và tồn kho giữ chỗ (Reservations).

2. Who owns price?
   -> Catalog Module sở hữu giá bán hiện tại (Current Selling Price).
   -> Order Module sở hữu giá mua lịch sử (Historical Purchase Price Snapshot).

3. Who can mutate Order.status?
   -> Chỉ duy nhất Order Module mới có quyền thay đổi trạng thái của đơn hàng (`Order.status`).

4. Does Cart store price?
   -> Cart có thể cache/hiển thị giá tạm thời phục vụ UI, nhưng KHÔNG PHẢI là Source of Truth về giá.

5. Checkout uses Cart price or Catalog price?
   -> Checkout BẮT BUỘC sử dụng giá niêm yết hiện tại từ Catalog Module tại thời điểm bấm đặt hàng.

6. Can Payment update Order directly?
   -> NO. Payment KHÔNG ĐƯỢC phép update bảng Order trực tiếp.
   -> Payment chỉ báo cáo kết quả thanh toán thông qua Boundary / Domain Event (`PaymentSucceeded`).
      Order Module nhận Event này và tự thực hiện State Transition hợp lệ sang `PAID`.

7. Can Seller update Inventory directly?
   -> NO. Seller KHÔNG ĐƯỢC phép ghi trực tiếp vào DB của Inventory.
   -> Seller chỉ gửi yêu cầu (Request) tới Inventory Module để thực hiện các thao tác thay đổi tồn kho.

8. Reservation succeeds but Create Order fails?
   -> Primary Compensation: CheckoutService cố gắng gọi `Inventory.ReleaseReservation()` ngay lập tức.
   -> Safety Mechanism (Crash Recovery): Nếu Process bị crash trước khi lệnh Release chạy,
      hệ thống Worker chạy ngầm của Inventory sẽ tự động giải phóng kho khi Reservation hết hạn TTL (15 phút).
===============================================================================
