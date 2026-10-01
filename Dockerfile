# syntax=docker/dockerfile:1

# ============================================================================
# 阶段 1：构建前端（React + Vite），产物输出到 Go 的 embed 目录
# ============================================================================
FROM node:24-alpine AS frontend
WORKDIR /src

# 先复制依赖清单，利用层缓存
COPY web-frontend/package.json web-frontend/package-lock.json ./web-frontend/
RUN cd web-frontend && npm ci

# 复制前端源码并构建（outDir 指向 ../mccms-go/internal/web/dist）
COPY web-frontend/ ./web-frontend/
RUN cd web-frontend && npm run build

# ============================================================================
# 阶段 2：构建后端（Go），内嵌前端产物
# ============================================================================
FROM golang:1.27-alpine AS backend
WORKDIR /src

# 先拉依赖，利用层缓存
COPY mccms-go/go.mod mccms-go/go.sum ./mccms-go/
RUN cd mccms-go && go mod download

# 复制后端源码 + 前端构建产物（覆盖本地 dist）
COPY mccms-go/ ./mccms-go/
COPY --from=frontend /src/mccms-go/internal/web/dist ./mccms-go/internal/web/dist

# 纯静态编译，产出单二进制
RUN cd mccms-go && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mccmsd ./cmd/mccmsd

# ============================================================================
# 阶段 3：运行时
# ============================================================================
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=backend /out/mccmsd /app/mccmsd

# 数据目录（账号库 / 卡密库 / 下载）统一挂载到 /data，便于持久化
RUN mkdir -p /data
ENV MCCMS_VIP_STORE=/data/vip.json

EXPOSE 8765

# 容器内必须监听 0.0.0.0（默认 127.0.0.1 在容器里不可达）
ENTRYPOINT ["/app/mccmsd"]
CMD ["-addr", "0.0.0.0:8765", "-accounts", "/data/accounts.json", "-download-dir", "/data/downloads"]

# 健康检查：后端自带 /api/health
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1:8765/api/health || exit 1