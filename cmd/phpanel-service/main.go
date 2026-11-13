package main

import (
	"flag"
	"log"
	"phpanel/internal/panel"
)

func main() {
	root := flag.String("root", "", "PHPanel data root")
	instance := flag.String("instance", "", "Instance ID")
	name := flag.String("name", "", "Windows service name")
	phpHost := flag.Bool("php-host", false, "Host a linked PHP runtime")
	owner := flag.String("owner", "", "Nginx instance ID")
	run := flag.String("run", "", "Nginx linkage token")
	flag.Parse()
	if *phpHost {
		if err := panel.RunLinkedPHPHost(*root, *instance, *owner, *run); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *root == "" || *instance == "" || *name == "" {
		log.Fatal("此程序由 Windows 服务控制管理器调用，请从 PHPanel 注册系统服务")
	}
	if err := panel.RunSystemServiceHost(*root, *instance, *name); err != nil {
		log.Fatal(err)
	}
}
