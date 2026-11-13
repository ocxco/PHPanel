package main

import (
	"flag"
	"fmt"
	"os"
	"phpanel/internal/release"
)

func main() {
	root := flag.String("root", "", "Installed application directory")
	mode := flag.String("mode", "update", "update or uninstall")
	cleanup := flag.Bool("cleanup", false, "Remove only owned current-user startup/PATH entries")
	flag.Parse()
	if *root == "" {
		fmt.Println("缺少安装目录参数")
		os.Exit(1)
	}
	var err error
	if *cleanup {
		err = release.Cleanup(*root)
	} else {
		err = release.Check(*root, *mode)
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
