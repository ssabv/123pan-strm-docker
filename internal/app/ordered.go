package app

import (
	"bytes"
	"encoding/json"
	"os"
)

// orderedJSON: 按固定键序序列化 JSON 对象，保证库文件字段顺序与原版 Python 一致
type orderedJSON []kv

type kv struct {
	key string
	val any
}

func (o orderedJSON) MarshalJSON() ([]byte, error) {
	buf := bytes.Buffer{}
	buf.WriteByte('{')
	for i, item := range o {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(item.key)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		vb, err := json.Marshal(item.val)
		if err != nil {
			return nil, err
		}
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// writeLibraryFile: 以原版键序写入库文件
func (c *Config) writeLibraryFile(p string, lib map[string]any) {
	libOrdered := orderedJSON{
		{"id", lib["id"]},
		{"name", lib["name"]},
		{"commonPath", lib["commonPath"]},
		{"createdAt", lib["createdAt"]},
		{"meta", lib["meta"]},
		{"files", lib["files"]},
		{"category", lib["category"]},
	}
	b, err := json.MarshalIndent(libOrdered, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(p, b, 0o644)
	c.invalidateLibSummary(p)
}
