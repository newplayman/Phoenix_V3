package main

import (
	"flag"
	"fmt"
	"log"

	"phoenix-v3/internal/config"
)

func main() {
	path := flag.String("config", "configs/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.LoadConfig(*path)
	if err != nil {
		log.Fatalf("配置校验失败: %v", err)
	}

	fmt.Printf("Config OK. schema=%s strategy=%s chains=%d pools=%d\n",
		cfg.SchemaVersion, cfg.StrategyVersion, len(cfg.Chains), len(cfg.Pools))
}
