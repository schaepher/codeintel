package main

import (
	"encoding/json"
	"log"
	"os"
)

// processArray 流式读取顶级 JSON 数组
func processArray(r *os.File) {
	dec := json.NewDecoder(r)
	t, err := dec.Token()
	if err != nil {
		log.Fatalf("读取数组开始失败: %v", err)
	}
	if delim, ok := t.(json.Delim); !ok || delim != '[' {
		log.Fatalf("输入不是 JSON 数组")
	}
	index := 0
	for dec.More() {
		index++
		var elem interface{}
		if err := dec.Decode(&elem); err != nil {
			log.Printf("第 %d 个元素解析失败: %v (跳过)", index, err)
			continue
		}
		collectTypes("[]", elem)
	}
	t, err = dec.Token()
	if err != nil {
		log.Fatalf("读取数组结束失败: %v", err)
	}
	if delim, ok := t.(json.Delim); !ok || delim != ']' {
		log.Fatalf("输入数组格式不正确")
	}
}