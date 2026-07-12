VPS_HOST  ?= 103.176.178.198
VPS_USER  ?= root
VPS_DIR   ?= /opt/bida
APP_NAME  ?= bida

.PHONY: dev build build-linux deploy logs restart stop

# Chạy local (cần postgres chạy sẵn)
dev:
	cd backend && go run .

# Build binary cho Linux (deploy trực tiếp, không dùng Docker)
build-linux:
	cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ../dist/bida-server .
	@echo "✓ Binary: dist/bida-server"

# Build Docker image local
build:
	docker build -t $(APP_NAME) .

# ─── Deploy lên VPS bằng Docker Compose ───────────────────────
deploy:
	@echo "→ Đồng bộ file lên VPS..."
	ssh $(VPS_USER)@$(VPS_HOST) "mkdir -p $(VPS_DIR)"
	rsync -avz --exclude='.git' --exclude='dist' \
		./ $(VPS_USER)@$(VPS_HOST):$(VPS_DIR)/
	@echo "→ Khởi động containers..."
	ssh $(VPS_USER)@$(VPS_HOST) "cd $(VPS_DIR) && \
		[ -f .env ] || cp .env.example .env && \
		docker compose pull postgres 2>/dev/null; \
		docker compose build app && \
		docker compose up -d"
	@echo "✓ Deploy thành công → http://$(VPS_HOST)"

# ─── Deploy bằng binary trực tiếp (không Docker) ──────────────
deploy-bin: build-linux
	@echo "→ Upload binary + frontend..."
	ssh $(VPS_USER)@$(VPS_HOST) "mkdir -p $(VPS_DIR)/frontend"
	scp dist/bida-server $(VPS_USER)@$(VPS_HOST):$(VPS_DIR)/
	rsync -avz website/ $(VPS_USER)@$(VPS_HOST):$(VPS_DIR)/frontend/
	@echo "→ Restart PM2..."
	ssh $(VPS_USER)@$(VPS_HOST) "cd $(VPS_DIR) && \
		pm2 delete $(APP_NAME) 2>/dev/null; \
		pm2 start ./bida-server --name $(APP_NAME) \
			--env production && pm2 save"
	@echo "✓ Deploy binary thành công"

# Setup nginx lần đầu
setup-nginx:
	scp nginx.conf $(VPS_USER)@$(VPS_HOST):/etc/nginx/sites-available/bida
	ssh $(VPS_USER)@$(VPS_HOST) "\
		ln -sf /etc/nginx/sites-available/bida /etc/nginx/sites-enabled/bida && \
		nginx -t && systemctl reload nginx"

logs:
	ssh $(VPS_USER)@$(VPS_HOST) "cd $(VPS_DIR) && docker compose logs -f app"

restart:
	ssh $(VPS_USER)@$(VPS_HOST) "cd $(VPS_DIR) && docker compose restart app"

stop:
	ssh $(VPS_USER)@$(VPS_HOST) "cd $(VPS_DIR) && docker compose down"
