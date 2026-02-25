package cmd

import (
	"bytes"
	"errors"
	"image"
	"image/png"

	"github.com/mattn/go-sixel"
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
