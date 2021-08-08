package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"ehang.io/nps/client"
	"ehang.io/nps/lib/common"
	"ehang.io/nps/lib/config"       //配置
	"ehang.io/nps/lib/file"         //文件读写
	"ehang.io/nps/lib/install"      //服务安装
	"ehang.io/nps/lib/version"      //版本号及最低版本要求
	"github.com/astaxie/beego/logs" //日志
	"github.com/ccding/go-stun/stun"
	"github.com/kardianos/service"
)

var (
	serverAddr     = flag.String("server", "", "Server addr (ip:port)")                               //服务器地址
	configPath     = flag.String("config", "", "Configuration file path")                             //配置文件路径
	verifyKey      = flag.String("vkey", "", "Authentication key")                                    //认证秘钥
	logType        = flag.String("log", "stdout", "Log output mode（stdout|file）")                     //日志输出模式
	connType       = flag.String("type", "tcp", "Connection type with the server（kcp|tcp）")           //与服务器的连接类型
	proxyUrl       = flag.String("proxy", "", "proxy socks5 url(eg:socks5://111:222@127.0.0.1:9007)") //socks5 代理 url
	logLevel       = flag.String("log_level", "7", "log level 0~7")                                   //日志级别
	registerTime   = flag.Int("time", 2, "register time long /h")                                     //注册时长
	localPort      = flag.Int("local_port", 2000, "p2p local port")                                   //p2p本地端口
	password       = flag.String("password", "", "p2p password flag")                                 //p2p密码标志
	target         = flag.String("target", "", "p2p target")                                          //p2p目标
	localType      = flag.String("local_type", "p2p", "p2p target")                                   //p2p本地类型
	logPath        = flag.String("log_path", "", "npc log path")                                      //日志路径
	debug          = flag.Bool("debug", true, "npc debug")                                            //debug
	pprofAddr      = flag.String("pprof", "", "PProf debug addr (ip:port)")
	stunAddr       = flag.String("stun_addr", "stun.stunprotocol.org:3478", "stun server address (eg:stun.stunprotocol.org:3478)")    //STUN 服务器地址
	ver            = flag.Bool("version", false, "show current version")                                                              //版本
	disconnectTime = flag.Int("disconnect_timeout", 60, "not receiving check packet times, until timeout will disconnect the client") //无响应超时时间
)

func main() {
	flag.Parse()                   //解析命令行
	logs.Reset()                   //重置日志
	logs.EnableFuncCallDepth(true) //启动日志
	logs.SetLogFuncCallDepth(3)
	if *ver {
		common.PrintVersion()
		return
	}
	if *logPath == "" {
		*logPath = common.GetNpcLogPath()
	}
	if common.IsWindows() { //是不是Windows系统
		*logPath = strings.Replace(*logPath, "\\", "\\\\", -1)
	}
	if *debug { //日志debug
		logs.SetLogger(logs.AdapterConsole, `{"level":`+*logLevel+`,"color":true}`) //console 接口
	} else {
		logs.SetLogger(logs.AdapterFile, `{"level":`+*logLevel+`,"filename":"`+*logPath+`","daily":false,"maxlines":100000,"color":true}`) //记录到文件
	}

	// service 初始化
	options := make(service.KeyValue) //服务配置
	svcConfig := &service.Config{
		Name:        "Npc",
		DisplayName: "nps内网穿透客户端",
		Description: "一款轻量级、功能强大的内网穿透代理服务器。支持tcp、udp流量转发，支持内网http代理、内网socks5代理，同时支持snappy压缩、站点保护、加密传输、多路复用、header修改等。支持web图形化管理，集成多用户模式。",
		Option:      options,
	}
	if !common.IsWindows() { //非Windows下
		svcConfig.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target syslog.target"}
		svcConfig.Option["SystemdScript"] = install.SystemdScript
		svcConfig.Option["SysvScript"] = install.SysvScript
	}
	for _, v := range os.Args[1:] { // 读取命令行第一个参数
		switch v {
		case "install", "start", "stop", "uninstall", "restart":
			continue
		}
		if !strings.Contains(v, "-service=") && !strings.Contains(v, "-debug=") {
			svcConfig.Arguments = append(svcConfig.Arguments, v)
		}
	}
	svcConfig.Arguments = append(svcConfig.Arguments, "-debug=false") //参数后增加 -debug=false
	prg := &npc{
		exit: make(chan struct{}),
	}
	s, err := service.New(prg, svcConfig) //创建服务
	if err != nil {
		logs.Error(err, "服务功能已禁用")
		run()
		// run without service
		wg := sync.WaitGroup{}
		wg.Add(1)
		wg.Wait()
		return
	}
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "status":
			if len(os.Args) > 2 {
				path := strings.Replace(os.Args[2], "-config=", "", -1)
				client.GetTaskStatus(path)
			}
		case "register":
			flag.CommandLine.Parse(os.Args[2:])
			client.RegisterLocalIp(*serverAddr, *verifyKey, *connType, *proxyUrl, *registerTime)
		case "update":
			install.UpdateNpc()
			return
		case "nat":
			c := stun.NewClient()
			c.SetServerAddr(*stunAddr)
			nat, host, err := c.Discover()
			if err != nil || host == nil {
				logs.Error("get nat type error", err)
				return
			}
			fmt.Printf("nat type: %s \npublic address: %s\n", nat.String(), host.String())
			os.Exit(0)
		case "start", "stop", "restart":
			// support busyBox and sysV, for openWrt
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				cmd := exec.Command("/etc/init.d/"+svcConfig.Name, os.Args[1])
				err := cmd.Run()
				if err != nil {
					logs.Error(err)
				}
				return
			}
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			return
		case "install":
			service.Control(s, "stop")
			service.Control(s, "uninstall")
			install.InstallNpc()
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				confPath := "/etc/init.d/" + svcConfig.Name
				os.Symlink(confPath, "/etc/rc.d/S90"+svcConfig.Name)
				os.Symlink(confPath, "/etc/rc.d/K02"+svcConfig.Name)
			}
			return
		case "uninstall":
			err := service.Control(s, os.Args[1])
			if err != nil {
				logs.Error("Valid actions: %q\n%s", service.ControlAction, err.Error())
			}
			if service.Platform() == "unix-systemv" {
				logs.Info("unix-systemv service")
				os.Remove("/etc/rc.d/S90" + svcConfig.Name)
				os.Remove("/etc/rc.d/K02" + svcConfig.Name)
			}
			return
		}
	}
	s.Run()
}

type npc struct {
	exit chan struct{}
}

func (p *npc) Start(s service.Service) error {
	go p.run()
	return nil
}
func (p *npc) Stop(s service.Service) error {
	close(p.exit)
	if service.Interactive() {
		os.Exit(0)
	}
	return nil
}

func (p *npc) run() error {
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			logs.Warning("npc: panic serving %v: %v\n%s", err, string(buf))
		}
	}()
	run()
	select {
	case <-p.exit:
		logs.Warning("stop...")
	}
	return nil
}

func run() {
	common.InitPProfFromArg(*pprofAddr)
	//p2p or secret command
	if *password != "" {
		commonConfig := new(config.CommonConfig)
		commonConfig.Server = *serverAddr
		commonConfig.VKey = *verifyKey
		commonConfig.Tp = *connType
		localServer := new(config.LocalServer)
		localServer.Type = *localType
		localServer.Password = *password
		localServer.Target = *target
		localServer.Port = *localPort
		commonConfig.Client = new(file.Client)
		commonConfig.Client.Cnf = new(file.Config)
		go client.StartLocalServer(localServer, commonConfig)
		return
	}
	env := common.GetEnvMap()
	if *serverAddr == "" {
		*serverAddr, _ = env["NPC_SERVER_ADDR"]
	}
	if *verifyKey == "" {
		*verifyKey, _ = env["NPC_SERVER_VKEY"]
	}
	logs.Info("客户端版本为：%s, 核心版本： %s", version.VERSION, version.GetVersion())
	if *verifyKey != "" && *serverAddr != "" && *configPath == "" {
		go func() {
			for {
				client.NewRPClient(*serverAddr, *verifyKey, *connType, *proxyUrl, nil, *disconnectTime).Start()
				logs.Info("客户端关闭！它将在五秒钟内重新连接")
				time.Sleep(time.Second * 5)
			}
		}()
	} else {
		if *configPath == "" {
			*configPath = common.GetConfigPath() //读取配置文件
		}
		go client.StartFromFile(*configPath)
	}
}
