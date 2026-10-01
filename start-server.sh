#!/bin/sh
# mccms 后端启动脚本：固定账号库、卡密库与爱发电回调配置。
# 用法：./start-server.sh [额外参数...]
set -e
ROOT="$(cd "$(dirname "$0")" && pwd)"
cd "$ROOT/mccms-go"

# 账号库与卡密库统一放在 mccms-go 目录下，避免重启后路径漂移。
export MCCMS_DATA_DIR="$PWD"
export MCCMS_VIP_STORE="$PWD/vip.json"

# ---- 爱发电凭据与通用配置从根目录 .env 读取 ----
if [ -f "$ROOT/.env" ]; then
  set -a
  . "$ROOT/.env"
  set +a
fi

# 每个订阅月折算的天数（默认 31）。
: "${MCCMS_AFDIAN_DAYS_PER_MONTH:=31}"

# Webhook 验签使用爱发电平台公钥（已内置），如需覆盖可设置：
#   MCCMS_AFDIAN_PUBKEY=/path/to/afdian_public.pem
# 仅本地联调时可设 MCCMS_AFDIAN_INSECURE=1 跳过验签，生产务必不要开启。

exec ./mccmsd -addr 127.0.0.1:8765 -accounts "$PWD/accounts.json" "$@"