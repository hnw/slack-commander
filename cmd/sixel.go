package cmd

import (
	"bytes"
	"errors"
	"image"
	"image/png"

	"github.com/mattn/go-sixel"
)

// sixelState は rawWriter 内の sixel ステートマシンの状態を表す
type sixelState int

const (
	sixelStateText    sixelState = iota // 通常テキスト
	sixelStateESC                       // \x1b を受信後（次のバイトで DCS か判断）
	sixelStateDCS                       // DCS パラメータ解析中（'q' で sixel 確定前）
	sixelStateDCSESC                    // DCS 中で \x1b を受信（ST の期待）
	sixelStateData                      // sixel データ受信中（'q' の後）
	sixelStateDataESC                   // sixel データ中で \x1b を受信（ST の期待）
)

// sixelToPNG は完全な DCS sixel シーケンス（\x1bP...\x1b\ を含む）を
// PNG エンコードされたバイト列に変換する。
func sixelToPNG(sixelData []byte) ([]byte, error) {
	decoder := sixel.NewDecoder(bytes.NewReader(sixelData))
	var img image.Image
	if err := decoder.Decode(&img); err != nil {
		return nil, err
	}
	if img == nil {
		return nil, errors.New("sixel decoder returned nil image")
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
