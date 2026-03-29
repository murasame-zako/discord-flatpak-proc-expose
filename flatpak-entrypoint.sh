#!/bin/bash
setsid -f $XDG_DATA_HOME/proc-expose client

function exit_handler(){
	pkill -9 procexpclient
	echo 收到指令，正在退出proc-expose客户端
}
trap 'exit_handler' EXIT TERM INT

/app/bin/com.discordapp.Discord $@