#!/bin/bash

# 定义可执行文件路径
PROC_EXPOSE_BIN="$XDG_DATA_HOME/proc-expose-amd64"
DISCORD_BIN="/app/bin/com.discordapp.Discord"

# 用于记录 proc-expose 的进程 ID
PROC_PID=""

# --------------------------------------------------------
# 当 Discord 退出，或者容器被系统强制关闭时，会触发此函数
# --------------------------------------------------------
cleanup() {
    if [ -n "$PROC_PID" ]; then
        echo "[Flatpak Entrypoint] 正在关闭 proc-expose 客户端 (PID: $PROC_PID)..."
        # 发送 SIGTERM 信号让其优雅退出
        kill -TERM "$PROC_PID" 2>/dev/null
        # 等待进程真正结束
        wait "$PROC_PID" 2>/dev/null
    fi
}

# 捕获 EXIT (脚本结束) 以及 INT/TERM (终止信号) 信号，触发 cleanup
trap cleanup EXIT INT TERM

# --------------------------------------------------------
# 1. 尝试启动 proc-expose 客户端
# --------------------------------------------------------
if [ -x "$PROC_EXPOSE_BIN" ]; then
    echo "[Flatpak Entrypoint] 启动 proc-expose 客户端..."
    # 在后台启动客户端，并记录 PID
    "$PROC_EXPOSE_BIN" client &
    PROC_PID=$!
else
    echo "[Flatpak Entrypoint] 警告: 未找到 proc-expose 或没有执行权限 ($PROC_EXPOSE_BIN)"
    echo "请确保文件存在并已执行 chmod +x"
fi

# --------------------------------------------------------
# 2. 启动 Discord 主程序 (保持在前台运行)
# --------------------------------------------------------
echo "[Flatpak Entrypoint] 启动 Discord..."

"$DISCORD_BIN" "$@"
DISCORD_EXIT_CODE=$?

# 当 Discord 退出后，脚本会运行到这里准备 exit
exit $DISCORD_EXIT_CODE
