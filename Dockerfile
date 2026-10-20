# syntax=docker/dockerfile:1
# anidock 单端口单镜像：Go 后端（纯 Go 无 CGO）+ 编译后的前端静态产物。
# 数据（DB/镜像文件/封面）全部落在 /app/data 与 /app/media，
# 用卷挂载后，升级镜像不会丢数据。

# ---- 阶段 1：前端构建 ----
# BASE_PATH：默认 '/'（根路径部署）；nginx 前缀分流时传 /anidock/，
# 例如 docker build --build-arg BASE_PATH=/anidock/ -t anidock:vX-prefix ...
FROM node:22-alpine AS frontend
ARG BASE_PATH=/
ENV ANIDOCK_BASE=${BASE_PATH}
WORKDIR /build
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# ---- 阶段 2：后端构建 ----
# 构建环境无法直连 proxy.golang.org 时，可追加：
#   docker build --build-arg GOPROXY=https://goproxy.cn,direct ...
FROM golang:1.27-alpine AS backend
ARG GOPROXY=
ENV GOPROXY=${GOPROXY}
# 版本号（仓库根 VERSION 文件）：docker build --build-arg VERSION=$(cat VERSION)
ARG VERSION=dev
WORKDIR /build
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /app/anidock . \
    && mkdir -p /app/data /app/media

# ---- 阶段 3：运行时（distroless static，无 shell，约 3MB 基础层）----
# 容器内用户为 nonroot (uid 9999)：
#   - 数据目录预建并 chown 给 nonroot，空命名卷挂载时会继承该属主，无需任何处理；
#   - 绑定宿主目录时如遇权限问题：sudo chown -R 9999:9999 ./data ./media
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
ARG VERSION=dev
LABEL org.opencontainers.image.title="anidock" \
      org.opencontainers.image.description="AniDock · 番港 —— 字幕组 RSS 番剧聚合站" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=backend /app/anidock /app/anidock
COPY --from=frontend /build/dist /app/dist
# 预建数据目录（属主 nonroot）：保证容器内可写，且空命名卷继承该属主
COPY --from=backend --chown=nonroot:nonroot /app/data /app/data
COPY --from=backend --chown=nonroot:nonroot /app/media /app/media
ENV PORT=8080 \
    DB_PATH=/app/data/db.sqlite \
    MEDIA_DIR=/app/media \
    MIRROR_DIR=/app/data/mirror \
    FRONTEND_DIST=/app/dist
VOLUME ["/app/data", "/app/media"]
EXPOSE 8080
WORKDIR /app
ENTRYPOINT ["/app/anidock"]
