FROM golang:alpine AS builder

# 替换镜像源
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories

RUN apk add --no-cache git ca-certificates build-base olm-dev

ENV GOPROXY=https://goproxy.cn,direct

WORKDIR /build

# Copy go mod files and download dependencies (cached layer)
COPY go.mod go.sum /build
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build
COPY . /build
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    ./build.sh

# 精简镜像
FROM alpine

# 目标运行用户（UID/GID）。Go 入口逻辑在运行时据此降权并 chown /data
ARG UID=1337
ARG GID=1337
ENV UID=$UID GID=$GID

# 替换镜像源
# 换源、安装必要包（仅保留 ca-certificates 与 olm 运行时依赖）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.tuna.tsinghua.edu.cn/g' /etc/apk/repositories \
    && apk add --no-cache \
        ca-certificates \
        olm \
    && rm -rf /var/cache/apk/*

# 从构建阶段复制二进制（入口逻辑已内置于 binary）
COPY --from=builder /build/matrix-pylon /usr/bin/matrix-pylon

WORKDIR /data
VOLUME /data

CMD ["/usr/bin/matrix-pylon"]