#!/bin/sh

# 替换 [[ ]] 为 [ ]，去掉 function 关键字
if [ -z "$GID" ]; then
    GID="$UID"
fi

# 定义函数（POSIX 风格）
fixperms() {
    chown -R "$UID:$GID" /data

    # /opt/matrix-pylon 只读，若日志指向该路径则禁用文件日志
    if [ "$(yq e '.logging.writers[1].filename' /data/config.yaml)" = "./logs/matrix-pylon.log" ]; then
        yq -I4 e -i 'del(.logging.writers[1])' /data/config.yaml
    fi
}

if [ ! -f /data/config.yaml ]; then
    /usr/bin/matrix-pylon -c /data/config.yaml -e
    echo "Didn't find a config file."
    echo "Copied default config file to /data/config.yaml"
    echo "Modify that config file to your liking."
    echo "Start the container again after that to generate the registration file."
    exit
fi

if [ ! -f /data/registration.yaml ]; then
    /usr/bin/matrix-pylon -g -c /data/config.yaml -r /data/registration.yaml || exit $?
    echo "Didn't find a registration file."
    echo "Generated one for you."
    echo "See https://docs.mau.fi/bridges/general/registering-appservices.html on how to use it."
    exit
fi

cd /data
fixperms
exec su-exec "$UID:$GID" /usr/bin/matrix-pylon