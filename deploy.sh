#!/bin/bash
# ─────────────────────────────────────────────────────────────
# deploy.sh — Script cài đặt & deploy Bida Manager lên VPS
# Chạy trên VPS: bash deploy.sh
# ─────────────────────────────────────────────────────────────
set -e

APP_DIR="/opt/bida"
APP_NAME="bida"

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  🎱 Bida Manager — VPS Setup Script"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"

# ── 1. Cài Docker nếu chưa có ──────────────────────────────
if ! command -v docker &>/dev/null; then
    echo "→ Cài Docker..."
    curl -fsSL https://get.docker.com | sh
    systemctl enable docker
    systemctl start docker
fi

# ── 2. Cài Docker Compose plugin nếu chưa có ─────────────
if ! docker compose version &>/dev/null; then
    echo "→ Cài Docker Compose plugin..."
    apt-get install -y docker-compose-plugin 2>/dev/null || \
    yum install -y docker-compose-plugin 2>/dev/null || true
fi

# ── 3. Cài Nginx nếu chưa có ──────────────────────────────
if ! command -v nginx &>/dev/null; then
    echo "→ Cài Nginx..."
    apt-get update -qq && apt-get install -y nginx 2>/dev/null || \
    yum install -y nginx 2>/dev/null || true
fi

# ── 4. Tạo thư mục app ────────────────────────────────────
mkdir -p "$APP_DIR"
cd "$APP_DIR"

# ── 5. Tạo .env nếu chưa có ──────────────────────────────
if [ ! -f .env ]; then
    echo "→ Tạo file .env..."
    cat > .env << ENVEOF
POSTGRES_PASSWORD=$(openssl rand -hex 16)
JWT_SECRET=$(openssl rand -hex 32)
PORT=3000
ENVEOF
    echo "⚠️  Đã tạo .env với mật khẩu ngẫu nhiên. Xem /opt/bida/.env"
fi

# ── 6. Build & start containers ───────────────────────────
echo "→ Build & start Docker containers..."
docker compose build
docker compose up -d

# ── 7. Cấu hình Nginx ─────────────────────────────────────
echo "→ Cấu hình Nginx..."
cat > /etc/nginx/sites-available/bida << 'NGINXEOF'
server {
    listen 80;
    server_name _;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
    gzip on;
    gzip_types text/plain text/css application/javascript application/json;

    location / {
        proxy_pass http://127.0.0.1:3000;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_cache_bypass $http_upgrade;
    }
}
NGINXEOF

ln -sf /etc/nginx/sites-available/bida /etc/nginx/sites-enabled/bida
rm -f /etc/nginx/sites-enabled/default
nginx -t && systemctl reload nginx

# ── 8. Done ───────────────────────────────────────────────
echo ""
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  ✅ Deploy thành công!"
echo ""
echo "  Tài khoản mặc định:"
echo "  - Admin : admin / admin123"
echo "  - Staff : nhanvien / nv123"
echo ""
echo "  ⚠️  Hãy đổi mật khẩu sau khi đăng nhập!"
echo "  → Truy cập: http://$(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
