# ── Stage 1: Build Go binary ──────────────────────────────────
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o bida-server .

# ── Stage 2: Runtime image ────────────────────────────────────
FROM alpine:3.19
RUN apk add --no-cache tzdata ca-certificates
ENV TZ=Asia/Ho_Chi_Minh
WORKDIR /app
COPY --from=builder /app/bida-server .
COPY website/ ./frontend/
EXPOSE 3000
CMD ["./bida-server"]
