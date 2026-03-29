package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type Config struct {
	Match[]string          `json:"match"`
	Ignores  []string          `json:"ignores"`
	Rewrites map[string]string `json:"rewrites"`
}

type Message struct {
	Action  string   `json:"action"`
	HostPID int      `json:"host_pid"`
	Name    string   `json:"name"`
	Cmdline []string `json:"cmdline"`
}

var (
	clients[]net.Conn
	clientsMutex sync.Mutex

	activeState = make(map[int]Message)
	activeMutex sync.RWMutex

	matchRegexes[]*regexp.Regexp
)

// 工具：取两个int的最小值
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// 核心机制：修改进程显示名
func setProcessName(name string) {
	nameBytes := append([]byte(name)[:min(len(name), 15)], 0)
	syscall.Syscall(syscall.SYS_PRCTL, syscall.PR_SET_NAME, uintptr(unsafe.Pointer(&nameBytes[0])), 0)
}

// =======================
//        服务端逻辑
// =======================

func broadcast(msg Message) {
	clientsMutex.Lock()
	defer clientsMutex.Unlock()

	data, _ := json.Marshal(msg)
	data = append(data, '\n')

	var activeClients[]net.Conn
	for _, conn := range clients {
		_, err := conn.Write(data)
		if err == nil {
			activeClients = append(activeClients, conn)
		} else {
			conn.Close()
		}
	}
	clients = activeClients
}

func readConfig() *Config {
	file, err := os.ReadFile("config.json")
	if err != nil {
		log.Fatalf("无法读取 config.json: %v", err)
	}
	var cfg Config
	if err := json.Unmarshal(file, &cfg); err != nil {
		log.Fatalf("解析配置失败: %v", err)
	}

	// 编译现代正则表达式
	for _, m := range cfg.Match {
		re, err := regexp.Compile(m)
		if err != nil {
			// 对于旧版配置中的非标准通配符给予自动修复和警告
			if m == "*.exe" {
				re = regexp.MustCompile(`(?i)\.exe$`)
				log.Printf("警告: 检测到配置中的 '*.exe' 不是标准正则，已自动替换为正则 '(?i)\\.exe$'")
			} else {
				log.Fatalf("配置中提供了无效的正则表达式 '%s': %v", m, err)
			}
		}
		matchRegexes = append(matchRegexes, re)
	}
	return &cfg
}

func isIgnored(name string, ignores[]string) bool {
	nameLower := strings.ToLower(name)
	for _, ig := range ignores {
		if strings.ToLower(ig) == nameLower {
			return true
		}
	}
	return false
}

func matchProcess(pid int, cfg *Config) (Message, bool) {
	cmdBytes, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return Message{}, false
	}
	cmdString := string(cmdBytes)

	// 防循环逻辑
	if strings.Contains(cmdString, "--host-spawned-this-process") {
		return Message{}, false
	}

	rawArgs := strings.Split(cmdString, "\x00")
	var cleanArgs[]string
	for _, arg := range rawArgs {
		if arg != "" {
			cleanArgs = append(cleanArgs, arg)
		}
	}

	commBytes, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	comm := strings.TrimSpace(string(commBytes))

	// 1. 重写规则
	if target, ok := cfg.Rewrites[comm]; ok {
		return Message{Name: target, Cmdline: cleanArgs}, true
	}

	// 2. 正则表达式规则匹配
	for _, re := range matchRegexes {
		// 判断 comm 名字是否命中正则
		if re.MatchString(comm) && !isIgnored(comm, cfg.Ignores) {
			return Message{Name: comm, Cmdline: cleanArgs}, true
		}
		// 遍历命令参数判断是否命中正则
		for _, arg := range cleanArgs {
			base := filepath.Base(strings.ReplaceAll(arg, "\\", "/"))
			if re.MatchString(base) && !isIgnored(base, cfg.Ignores) {
				return Message{Name: base, Cmdline: cleanArgs}, true
			}
		}
	}
	return Message{}, false
}

func scanLoop(cfg *Config) {
	ticker := time.NewTicker(2 * time.Second)

	for range ticker.C {
		current := make(map[int]Message)
		dirs, _ := os.ReadDir("/proc")

		for _, d := range dirs {
			if !d.IsDir() {
				continue
			}
			pid, err := strconv.Atoi(d.Name())
			if err != nil {
				continue
			}

			if msg, ok := matchProcess(pid, cfg); ok {
				current[pid] = msg
			}
		}

		activeMutex.Lock()
		for pid, msg := range current {
			oldMsg, exists := activeState[pid]
			if !exists || oldMsg.Name != msg.Name {
				log.Printf("[启动] 发现进程 PID:%d, 伪装为:%s", pid, msg.Name)
				msg.Action = "start"
				msg.HostPID = pid
				broadcast(msg)
			}
		}
		for pid := range activeState {
			if _, exists := current[pid]; !exists {
				log.Printf("[停止] 进程结束 PID:%d", pid)
				broadcast(Message{Action: "stop", HostPID: pid})
			}
		}
		activeState = current
		activeMutex.Unlock()
	}
}

func handleClient(conn net.Conn) {
	log.Println("新的客户端已连接 (来自容器内)")
	clientsMutex.Lock()
	clients = append(clients, conn)
	clientsMutex.Unlock()

	activeMutex.RLock()
	for pid, msg := range activeState {
		msg.Action = "start"
		msg.HostPID = pid
		data, _ := json.Marshal(msg)
		conn.Write(append(data, '\n'))
	}
	activeMutex.RUnlock()

	bufio.NewReader(conn).ReadString('\n')
	log.Println("客户端已断开")
}

func runServer(customSockPath string) {
	setProcessName("procexpserver")
	cfg := readConfig()
	log.Printf("服务端启动，加载规则成功 (正则规则:%d, 忽略列表:%d, 重写规则:%d)...",
		len(matchRegexes), len(cfg.Ignores), len(cfg.Rewrites))

	var sockPath string
	if customSockPath != "" {
		sockPath = customSockPath
		// 确保自定义路径的父目录存在
		os.MkdirAll(filepath.Dir(sockPath), 0755)
	} else {
		xdg := os.Getenv("XDG_RUNTIME_DIR")
		if xdg == "" {
			log.Fatal("XDG_RUNTIME_DIR 环境变量未设置")
		}
		sockDir := filepath.Join(xdg, "app", "com.discordapp.Discord")
		os.MkdirAll(sockDir, 0755)
		sockPath = filepath.Join(sockDir, "proc-expose-server.sock")
	}

	os.Remove(sockPath)

	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		log.Fatalf("无法监听 Socket: %v", err)
	}
	defer listener.Close()
	log.Printf("监听 Socket: %s", sockPath)

	go scanLoop(cfg)

	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleClient(conn)
	}
}

// =======================
//        客户端逻辑
// =======================

func dummyProcess() {
	name := os.Getenv("FAKE_PROCESS_NAME")
	if name != "" {
		setProcessName(name)
	}
	// 无限阻塞，保持存活
	select {}
}

func checkRuntimeEnvironment(checks string) {
	dirs, _ := os.ReadDir("/proc")
	for _, d := range dirs {
		if d.IsDir() {
			comm, err := os.ReadFile("/proc/" + d.Name() + "/comm")
			switch checks{
			case "runInSameNamespace":
				if err == nil && strings.TrimSpace(string(comm)) == "procexpserver" {
					fmt.Println("错误：你在与服务端相同的空间（宿主机）运行了客户端！")
					os.Exit(1)
				}
			case "multiInstanceServer":
				if err == nil && strings.TrimSpace(string(comm)) == "procexpserver" {
					fmt.Println("错误：禁止多开服务端喵")
					os.Exit(1)
				}
			case "multiInstanceClient":
				if err == nil && strings.TrimSpace(string(comm)) == "procexpclient" {
					fmt.Println("错误：禁止多开客户端喵")
					os.Exit(1)
				}
			}
		}
	}
}

func runClient(customSockPath string) {
	checkRuntimeEnvironment("runInSameNamespace")
	checkRuntimeEnvironment("multiInstanceClient")
	setProcessName("procexpclient")
	
	var sockPaths []string

	if customSockPath != "" {
		sockPaths =[]string{customSockPath}
	} else {
		xdg := os.Getenv("XDG_RUNTIME_DIR")
		if xdg == "" {
			log.Fatal("XDG_RUNTIME_DIR 环境变量未设置")
		} 
		sockPaths =[]string{
			filepath.Join(xdg, "proc-expose-server.sock"),
			filepath.Join(xdg, "app", "com.discordapp.Discord", "proc-expose-server.sock"),
		}

	}

	selfExe, err := os.Executable()
	if err != nil {
		log.Fatalf("无法获取当前执行路径: %v", err)
	}
	
	fakeDir := filepath.Join(os.TempDir(), "proc-expose-wine")
	os.MkdirAll(fakeDir, 0755)
	fakeWinePreloader := filepath.Join(fakeDir, "wine-preloader")

	// 复制合并后的可执行文件本身至模拟器目录
	input, _ := os.ReadFile(selfExe)
	os.WriteFile(fakeWinePreloader, input, 0755)

	for {
		var conn net.Conn
		for _, sp := range sockPaths {
			conn, err = net.Dial("unix", sp)
			if err == nil {
				log.Printf("成功连接到服务端 Socket: %s", sp)
				break
			}
		}

		if conn == nil {
			log.Printf("连接服务端失败: %v, 5秒后重试...", err)
			time.Sleep(5 * time.Second)
			continue
		}

		activeProcesses := make(map[int]*exec.Cmd)
		scanner := bufio.NewScanner(conn)

		for scanner.Scan() {
			var msg Message
			if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
				continue
			}

			if msg.Action == "start" {
				if _, exists := activeProcesses[msg.HostPID]; exists {
					continue
				}
				log.Printf("收到指令: 拉起虚拟进程 %s (对应宿主PID: %d)", msg.Name, msg.HostPID)

				cmdArgs := append(msg.Cmdline, "--host-spawned-this-process")
				if len(msg.Cmdline) == 0 {
					cmdArgs =[]string{msg.Name, "--host-spawned-this-process"}
				}

				// 咱笨比，懒得解决为什么虚拟进程总是缺少windows盘符特征这个问题，索性直接写死逻辑了。
				for i, arg := range cmdArgs {
					if strings.HasPrefix(arg, "\\") {
						cmdArgs[i] = "Z:" + arg
					}
				}

				// 由于 fakeWinePreloader 实际是我们的主程序副本，传入了 --host-spawned-this-process 标志位，
				// 它拉起后就会命中 main() 开头的检测，执行 dummyProcess()。
				cmd := &exec.Cmd{
					Path: fakeWinePreloader,
					Args: cmdArgs,
					SysProcAttr: &syscall.SysProcAttr{
						Pdeathsig: syscall.SIGKILL, // 当父进程退出时，内核自动给子进程发送 SIGKILL
					},
				}

				cmd.Env = append(os.Environ(),
					"FAKE_PROCESS_NAME="+msg.Name,
					"SteamClientLaunch=1",
					"SteamEnv=1",
					"SteamOS=1",
				)

				if err := cmd.Start(); err == nil {
					activeProcesses[msg.HostPID] = cmd
				} else {
					log.Printf("启动虚拟进程失败: %v", err)
				}

			} else if msg.Action == "stop" {
				if cmd, exists := activeProcesses[msg.HostPID]; exists {
					log.Printf("收到指令: 关闭虚拟进程 (对应宿主PID: %d)", msg.HostPID)
					cmd.Process.Kill()
					cmd.Process.Wait()
					delete(activeProcesses, msg.HostPID)
				}
			}
		}

		log.Println("与服务端的连接已断开，清理所有的本地虚拟进程...")
		for pid, cmd := range activeProcesses {
			cmd.Process.Kill()
			cmd.Process.Wait()
			delete(activeProcesses, pid)
		}
		conn.Close()
		time.Sleep(3 * time.Second)
	}
}

// =======================
//        主函数入口
// =======================

func main() {
	// 无论以何种方式拉起，只要包含此特殊标记位，代表属于要被 Discord 识别的虚拟进程
	for _, arg := range os.Args {
		if arg == "--host-spawned-this-process" {
			dummyProcess()
			return
		}
	}

	if len(os.Args) < 2 {
		fmt.Println("用法: proc-expose [server | client] [可选: Socket路径]")
		os.Exit(1)
	}

	// 获取可选的自定义 Socket 路径
	customSockPath := ""
	if len(os.Args) >= 3 {
		customSockPath = os.Args[2]
	}

	switch os.Args[1] {
	case "server":
		runServer(customSockPath)
	case "client":
		runClient(customSockPath)
	default:
		fmt.Println("用法: proc-expose [server | client] [可选: Socket路径]")
		os.Exit(1)
	}
}