package main

import (
	"bufio"
	"encoding/json"
	"log"
	"os"
	"strings"
)

// processJSONL 逐行读取 JSONL
func processJSONL(r *os.File) {
	scanner := bufio.NewScanner(r)
	const maxCapacity = 1024 * 1024
	buf := make([]byte, maxCapacity)
	scanner.Buffer(buf, maxCapacity)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			log.Printf("第 %d 行 JSON 解析失败: %v (跳过)", lineNum, err)
			continue
		}
		collectTypes("", obj)
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("读取文件出错: %v", err)
	}
}