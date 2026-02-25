package cmd

import (
	"bytes"
	"image"
	_ "image/png"
	"testing"
)

// minimalRedSixel は 4x6 ピクセルの赤い矩形を表す最小限の正しい sixel シーケンス
//
//	\x1bPq         = DCS ヘッダー（パラメータなし）
//	"1;1;4;6       = ラスター属性（縦横比 1:1、幅 4、高さ 6）
//	#1;2;100;0;0   = カラー 1 を RGB(100%, 0%, 0%) として定義
//	#1             = カラー 1 を選択
//	~~~~           = 4 列すべてで 6 行すべてがセット（~ = 63+63 = 全6行）
//	\x1b\          = ST ターミネーター
var minimalRedSixel = []byte("\x1bPq\"1;1;4;6#1;2;100;0;0#1~~~~\x1b\\")

// collectRawOutputs は rawWriter に対して複数回 Write して、Flush 後にチャンネルから全出力を収集する
func collectRawOutputs(t *testing.T, writes ...[]byte) []*CommandOutput {
	t.Helper()
	ch := make(chan *CommandOutput, 100)
	raw := newRawWriter(ch, nil, nil, false)
	for _, data := range writes {
		if _, err := raw.Write(data); err != nil {
			t.Fatalf("rawWriter.Write error: %v", err)
		}
	}
	if err := raw.Flush(); err != nil {
		t.Fatalf("rawWriter.Flush error: %v", err)
	}
	close(ch)
	var out []*CommandOutput
	for o := range ch {
		out = append(out, o)
	}
	return out
}

// TestRawWriterPureText は sixel なしのテキストが 1 件のテキスト出力として届くことを確認する
func TestRawWriterPureText(t *testing.T) {
	outs := collectRawOutputs(t, []byte("hello world"))
	if len(outs) != 1 {
		t.Fatalf("expected 1 output, got %d", len(outs))
	}
	if outs[0].Text != "hello world" {
		t.Errorf("expected 'hello world', got %q", outs[0].Text)
	}
	if outs[0].ImageData != nil {
		t.Error("expected no ImageData")
	}
}

// TestRawWriterSixelOnly は sixel のみの出力が ImageData として届くことを確認する
func TestRawWriterSixelOnly(t *testing.T) {
	outs := collectRawOutputs(t, minimalRedSixel)
	var imgOuts []*CommandOutput
	for _, o := range outs {
		if o.ImageData != nil {
			imgOuts = append(imgOuts, o)
		}
		if o.Text != "" {
			t.Errorf("unexpected text output: %q", o.Text)
		}
	}
	if len(imgOuts) != 1 {
		t.Fatalf("expected 1 image output, got %d", len(imgOuts))
	}
	if !bytes.HasPrefix(imgOuts[0].ImageData, []byte("\x89PNG")) {
		t.Errorf("ImageData does not look like PNG")
	}
}

// TestRawWriterTextBeforeAndAfterSixel はテキスト + sixel + テキストが正しく分離されることを確認する
func TestRawWriterTextBeforeAndAfterSixel(t *testing.T) {
	data := append([]byte("before\n"), minimalRedSixel...)
	data = append(data, []byte("\nafter")...)
	outs := collectRawOutputs(t, data)

	var texts []string
	var imgs []*CommandOutput
	for _, o := range outs {
		if o.Text != "" {
			texts = append(texts, o.Text)
		}
		if o.ImageData != nil {
			imgs = append(imgs, o)
		}
	}
	if len(imgs) != 1 {
		t.Errorf("expected 1 image, got %d", len(imgs))
	}
	allText := ""
	for _, t2 := range texts {
		allText += t2
	}
	if !bytes.Contains([]byte(allText), []byte("before\n")) {
		t.Errorf("missing 'before' text, got: %q", allText)
	}
	if !bytes.Contains([]byte(allText), []byte("\nafter")) {
		t.Errorf("missing 'after' text, got: %q", allText)
	}
}

// TestRawWriterSixelSpanningTwoWrites は sixel が複数 Write() 呼び出しをまたいでも正しく処理されることを確認する
func TestRawWriterSixelSpanningTwoWrites(t *testing.T) {
	half := len(minimalRedSixel) / 2
	outs := collectRawOutputs(t, minimalRedSixel[:half], minimalRedSixel[half:])

	var imgOuts []*CommandOutput
	for _, o := range outs {
		if o.ImageData != nil {
			imgOuts = append(imgOuts, o)
		}
	}
	if len(imgOuts) != 1 {
		t.Fatalf("expected 1 image output spanning two writes, got %d", len(imgOuts))
	}
}

// TestRawWriterMultipleSixels は複数の sixel シーケンスがそれぞれ別の ImageData として届くことを確認する
func TestRawWriterMultipleSixels(t *testing.T) {
	combined := append(minimalRedSixel, minimalRedSixel...)
	outs := collectRawOutputs(t, combined)

	var imgOuts []*CommandOutput
	for _, o := range outs {
		if o.ImageData != nil {
			imgOuts = append(imgOuts, o)
		}
	}
	if len(imgOuts) != 2 {
		t.Fatalf("expected 2 image outputs, got %d", len(imgOuts))
	}
}

// TestRawWriterIncompleteSixelDiscarded は末尾が切れた sixel が破棄されることを確認する
func TestRawWriterIncompleteSixelDiscarded(t *testing.T) {
	incomplete := minimalRedSixel[:len(minimalRedSixel)-2]
	outs := collectRawOutputs(t, incomplete)

	for _, o := range outs {
		if o.ImageData != nil {
			t.Error("incomplete sixel should be discarded, but got ImageData")
		}
	}
}

// TestRawWriterNonSixelDCS は非 sixel DCS（'q' なし）が破棄されることを確認する
func TestRawWriterNonSixelDCS(t *testing.T) {
	nonSixelDCS := []byte("\x1bPfoo\x1b\\")
	outs := collectRawOutputs(t, nonSixelDCS)

	for _, o := range outs {
		if o.ImageData != nil {
			t.Error("non-sixel DCS should be discarded")
		}
	}
}

// TestSixelToPNGRoundTrip は sixelToPNG が正しい PNG を返すことを確認する
func TestSixelToPNGRoundTrip(t *testing.T) {
	pngBytes, err := sixelToPNG(minimalRedSixel)
	if err != nil {
		t.Fatalf("sixelToPNG returned error: %v", err)
	}
	if pngBytes == nil {
		t.Fatal("sixelToPNG returned nil")
	}
	if !bytes.HasPrefix(pngBytes, []byte("\x89PNG")) {
		t.Error("result does not have PNG magic bytes")
	}
	decoded, format, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("image.Decode failed: %v", err)
	}
	if format != "png" {
		t.Errorf("expected png format, got %q", format)
	}
	if decoded.Bounds().Dx() == 0 || decoded.Bounds().Dy() == 0 {
		t.Error("decoded image has zero dimensions")
	}
}

// TestSixelToPNGInvalid は不正な sixel データで sixelToPNG がエラーを返すことを確認する
func TestSixelToPNGInvalid(t *testing.T) {
	result, err := sixelToPNG([]byte("not sixel data"))
	if result != nil {
		t.Error("expected nil bytes for invalid sixel data")
	}
	if err == nil {
		t.Error("expected error for invalid sixel data")
	}
}
