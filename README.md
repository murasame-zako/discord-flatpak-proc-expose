# 介绍

> flatpak隔离性能太好了，导致一些不支持RPC的游戏没法显示富状态，因为discord在容器内无法窥探宿主机进程信息。

> 咱的思路是搞个服务端客户端，服务器扫描进程列表，发送cmdline给容器内的客户端，客户端再自己拉起一个虚假进程自己给自己改名。虽然这是一个很丑陋的解决方案，但是可以忽悠discord启动了游戏程序。

![使用flatpak版本的discord成功读取到了不支持RPC的游戏状态](https://maru.ciallo.work/2026/03/1774963749-Screenshot_2026-03-31_21-27-01.png "使用flatpak版本的discord成功读取到了不支持RPC的游戏状态")

# 使用方法

将 flatpak-entrypoint.sh 和 对应的二进制可执行文件复制到 `$HOME/.var/app/com.discordapp.Discord/data/`

在主机上（不是在容器内）启动服务端进程，以实时检测进程列表：

`$HOME/.var/app/com.discordapp.Discord/data/proc-expose-amd64 client
`

然后修改discord的快捷方式，修改命令行参数以使用以下的命令参数启动flatpak版本的Discord，让客户端进程随着Discord启动即可开始使用。

`/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=$HOME/.var/app/com.discordapp.Discord/data/flatpak-entrypoint.sh --file-forwarding com.discordapp.Discord @@u %U @@`

如果您希望开启自启动discord到系统托盘，将以下命令添加到开机自启动项目即可。

`/usr/bin/flatpak run --branch=stable --arch=x86_64 --command=$HOME/.var/app/com.discordapp.Discord/data/flatpak-entrypoint.sh --file-forwarding com.discordapp.Discord --start-minimized`

# 安全风险

任何可以与服务端的unix域： `$XDG_RUNTIME_DIR/app/com.discordapp.Discord/proc-expose-server.sock` 连接的应用程序都可以读取到被暴露给容器内的进程列表，不过本来在非容器内的情况下任何软件都能查看进程列表，应该对安全影响不大。

# 需要改进的地方

由于为了摆脱glibc版本的限制，禁用了CGO，导致即使每个被生成的虚假进程都需要占用大约4MB的RAM。不过由于内置规则只会发生包含 `.exe`  关键字的进程，减少了很多进程数量，应该影响不大。

几乎纯ai编写，毕竟咱真不懂编程(摆)。


# 另请参阅

https://github.com/EnderIce2/rpc-bridge

https://github.com/LeadRDRK/wine-rpc