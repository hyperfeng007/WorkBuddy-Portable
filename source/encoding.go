package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
)

// Decode editor-created config text explicitly; never guess legacy code pages.
func decodeConfigText(b []byte) ([]byte, error) {
	if bytes.HasPrefix(b, []byte{0xff, 0xfe, 0, 0}) || bytes.HasPrefix(b, []byte{0, 0, 0xfe, 0xff}) {
		return nil, fmt.Errorf("不支持 UTF-32，请将配置保存为 UTF-8 或带 BOM 的 UTF-16")
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	if bytes.HasPrefix(b, []byte{0xff, 0xfe}) || bytes.HasPrefix(b, []byte{0xfe, 0xff}) {
		var order binary.ByteOrder = binary.LittleEndian
		if b[0] == 0xfe {
			order = binary.BigEndian
		}
		b = b[2:]
		if len(b)%2 != 0 {
			return nil, fmt.Errorf("UTF-16 配置长度不完整，请重新保存文件")
		}
		units := make([]uint16, len(b)/2)
		for i := range units {
			units[i] = order.Uint16(b[2*i:])
		}
		for i := 0; i < len(units); i++ {
			u := units[i]
			if u >= 0xd800 && u <= 0xdbff {
				if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
					return nil, fmt.Errorf("UTF-16 配置包含无效代理项")
				}
				i++
			} else if u >= 0xdc00 && u <= 0xdfff {
				return nil, fmt.Errorf("UTF-16 配置包含孤立代理项")
			}
		}
		b = []byte(string(utf16.Decode(units)))
	}
	if !utf8.Valid(b) {
		return nil, fmt.Errorf("配置不是有效 UTF-8 / 带 BOM 的 UTF-16；请在编辑器中另存为 UTF-8（不要使用 ANSI / GBK）")
	}
	return b, nil
}
func utf16Units(s string) int { return len(utf16.Encode([]rune(s))) }
