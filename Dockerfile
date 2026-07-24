FROM golang:alpine AS builder

# 替换为阿里云镜像源（或者 mirrors.tuna.tsinghua.edu.cn）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

RUN apk add --no-cache git ca-certificates build-base su-exec olm-dev

ENV GOPROXY=https://goproxy.cn,direct

COPY . /build
WORKDIR /build
RUN ./build.sh


# 精简镜像
FROM alpine

ENV UID=1337 GID=1337

# 替换为阿里云镜像源（或者 mirrors.tuna.tsinghua.edu.cn）
# 换源、安装必要包
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories \
    && apk add --no-cache \
        su-exec \
        ca-certificates \
        olm \
        yq-go \
    && rm -rf /var/cache/apk/*

# 从构建阶段复制二进制和修改后的启动脚本
COPY --from=builder /build/matrix-pylon /usr/bin/matrix-pylon
COPY --from=builder /build/docker-run.sh /docker-run.sh

# 赋予执行权限
RUN chmod +x /docker-run.sh

WORKDIR /data
VOLUME /data

CMD ["/docker-run.sh"]