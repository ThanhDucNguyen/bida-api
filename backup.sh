#!/bin/bash
# ─────────────────────────────────────────────────────────────
# backup.sh — Sao lưu database Bida Manager
# Cài cron chạy hằng đêm:  crontab -e
#   0 3 * * * /opt/bida/backup.sh >> /var/log/bida-backup.log 2>&1
# ─────────────────────────────────────────────────────────────
set -e

BACKUP_DIR="/opt/bida/backups"
KEEP_DAYS=30
STAMP=$(date +%Y%m%d_%H%M%S)
FILE="$BACKUP_DIR/bida_$STAMP.sql.gz"

mkdir -p "$BACKUP_DIR"

# Dump + nén
docker exec bida-postgres pg_dump -U bida bida | gzip > "$FILE"

# Kiểm tra file không rỗng (dump lỗi sẽ ra file ~0 byte)
SIZE=$(stat -c%s "$FILE")
if [ "$SIZE" -lt 1000 ]; then
    echo "[$(date)] ❌ BACKUP LỖI — file chỉ $SIZE bytes: $FILE"
    rm -f "$FILE"
    exit 1
fi

# Xoá bản cũ hơn KEEP_DAYS ngày
find "$BACKUP_DIR" -name "bida_*.sql.gz" -mtime +$KEEP_DAYS -delete

echo "[$(date)] ✓ Backup OK: $FILE ($(du -h "$FILE" | cut -f1))"
