# 🎱 Bida Manager — Full Stack

Hệ thống quản lý câu lạc bộ bida. Go + PostgreSQL + WebSocket.

## Cấu trúc

```
bida/
├── backend/          ← Go REST API + WebSocket
│   ├── main.go
│   └── go.mod
├── website/          ← Frontend HTML (staff + owner)
│   ├── index.html
│   ├── login.html
│   ├── staff.html
│   └── owner.html
├── docker-compose.yml
├── Dockerfile
├── nginx.conf
├── Makefile
└── deploy.sh
```

## Tài khoản mặc định

| Role | Username | Password |
|------|----------|----------|
| Admin (Chủ quán) | `admin` | `admin123` |
| Staff (Nhân viên) | `nhanvien` | `nv123` |

⚠️ **Đổi mật khẩu ngay sau khi deploy!**

---

## Deploy nhanh lên VPS

### Cách 1: Docker Compose (khuyến nghị)

```bash
# 1. Clone / upload code lên VPS
scp -r . root@103.176.178.198:/opt/bida

# 2. SSH vào VPS
ssh root@103.176.178.198

# 3. Chạy script cài đặt
cd /opt/bida
bash deploy.sh
```

Hoặc dùng Makefile từ máy local:
```bash
make deploy
```

### Cách 2: Binary trực tiếp (không Docker)

```bash
# Cần Go 1.21+ và PostgreSQL trên VPS

# Build binary cho Linux
make build-linux

# Deploy
make deploy-bin
```

---

## Chạy local (development)

```bash
# Cài PostgreSQL local, tạo database
createdb bida
createuser bida -P  # đặt password mạnh, rồi ghi vào .env

# Cài Go dependencies
cd backend
go mod tidy

# Chạy server (tự tạo schema + seed data)
go run .

# Mở browser
open http://localhost:3000
```

---

## API Endpoints

| Method | Path | Mô tả |
|--------|------|-------|
| POST | `/api/auth/login` | Đăng nhập → JWT |
| GET | `/api/settings` | Cài đặt |
| PUT | `/api/settings` | Cập nhật cài đặt |
| GET | `/api/bida/state` | Trạng thái tất cả bàn |
| PUT | `/api/bida/:id/state` | Cập nhật bàn bida |
| GET/POST | `/api/tabs` | Tab khách |
| PUT/DELETE | `/api/tabs/:id` | Sửa/xóa tab |
| GET/POST | `/api/sessions` | Lịch sử thanh toán |
| GET/POST | `/api/inventory` | Sản phẩm |
| PUT/DELETE | `/api/inventory/:id` | Sửa/xóa sản phẩm |
| GET/POST | `/api/imports` | Nhập hàng |
| GET/POST | `/api/debts` | Công nợ |
| POST | `/api/debts/:id/pay` | Ghi nhận trả nợ |
| GET/POST | `/api/adjustments` | Điều chỉnh |
| DELETE | `/api/adjustments/:id` | Xóa điều chỉnh |
| GET/POST | `/api/expenses` | Chi phí vận hành |
| PUT/DELETE | `/api/expenses/:id` | Sửa/xóa chi phí |
| GET/POST | `/api/users` | Người dùng (admin) |
| PUT/DELETE | `/api/users/:id` | Sửa/xóa user (admin) |
| WS | `/ws?token=<jwt>` | WebSocket realtime |

---

## Environment Variables

```env
DATABASE_URL=postgres://bida:password@localhost:5432/bida?sslmode=disable
PORT=3000
JWT_SECRET=change-this-long-random-string
FRONTEND_DIR=./frontend
```
