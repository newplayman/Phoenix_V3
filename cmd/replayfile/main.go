package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"phoenix-v3/internal/config"
	"phoenix-v3/internal/events"
)

type fileEvent struct {
	Topic     events.Topic    `json:"topic"`
	Timestamp time.Time       `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func main() {
	configPath := flag.String("config", "", "可选：读取 configs/config.yaml 获取 events.file_path")
	path := flag.String("path", "", "events jsonl 文件路径")
	topics := flag.String("topics", "", "过滤 topic（逗号分隔），为空表示不过滤")
	follow := flag.Bool("follow", false, "持续跟随输出（类似 tail -f）")
	flag.Parse()

	if *configPath != "" {
		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			log.Fatalf("读取 config 失败: %v", err)
		}
		if *path == "" {
			*path = cfg.Events.FilePath
		}
	}
	if *path == "" {
		*path = "logs/events.jsonl"
	}

	allowed := map[string]struct{}{}
	if strings.TrimSpace(*topics) != "" {
		for _, t := range strings.Split(*topics, ",") {
			t = strings.TrimSpace(t)
			if t == "" {
				continue
			}
			allowed[t] = struct{}{}
		}
	}

	f, err := os.Open(*path)
	if err != nil {
		log.Fatalf("open %s failed: %v", *path, err)
	}
	defer f.Close()

	printLine := func(line []byte) {
		var ev fileEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return
		}
		if len(allowed) > 0 {
			if _, ok := allowed[string(ev.Topic)]; !ok {
				return
			}
		}
		fmt.Println(string(line))
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		b := append([]byte(nil), scanner.Bytes()...)
		printLine(b)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("scan failed: %v", err)
	}

	if !*follow {
		return
	}

	// follow
	for {
		pos, _ := f.Seek(0, io.SeekCurrent)
		stat, err := f.Stat()
		if err != nil {
			return
		}
		if stat.Size() < pos {
			// rotated/truncated
			_, _ = f.Seek(0, 0)
		}
		scanner = bufio.NewScanner(f)
		for scanner.Scan() {
			b := append([]byte(nil), scanner.Bytes()...)
			printLine(b)
		}
		time.Sleep(250 * time.Millisecond)
	}
}
