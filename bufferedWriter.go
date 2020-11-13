package main

import (
	"bytes"
	"fmt"
	"time"
)

type bufferedWriter struct {
	buffer bytes.Buffer
	writer func(string)
	timer  *time.Timer
}

func newBufferedWriter(writer func(string)) *bufferedWriter {
	return &bufferedWriter{
		writer: writer,
	}
}

func (b *bufferedWriter) Write(data []byte) (n int, err error) {
	//fmt.Printf("len=%d\n", len(data))
	if b.timer != nil {
		b.timer.Stop()
	}
	l := b.buffer.Len() + len(data)
	i := 0
	for l > 2000 {
		writeSize := 2000
		//fmt.Printf("====================\n")
		if b.buffer.Len() > 0 {
			//fmt.Printf("%s", b.buffer.Bytes())
			writeSize -= b.buffer.Len()
			b.buffer.Truncate(0)
		}
		hunk := data[i : i+writeSize]
		text := fmt.Sprintf("%s%s", b.buffer.Bytes(), hunk)
		b.writer(text)
		i += writeSize
		l -= 2000
	}
	n, err = b.buffer.Write(data[i:])
	// 最後に出力されてから3秒間何も出力されなければflashする
	b.timer = time.AfterFunc(3*time.Second, func() {
		b.timer.Stop()
		b.Flash()
	})
	n += i //今回のWriteで書き込まれた総バイト数
	return
}

func (b *bufferedWriter) Flash() {
	text := fmt.Sprintf("%s", b.buffer.Bytes())
	if len(text) > 0 {
		b.buffer.Truncate(0)
		b.writer(text)
	}
}
