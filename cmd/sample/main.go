package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

// Creates a tiny YOLO zip (images/ + labels/ + classes.txt) for Bruno.
func main() {
	out := "bruno/yolo-dataset/sample/sample_dataset.zip"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		panic(err)
	}
	f, err := os.Create(out)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)

	classes, err := zw.Create("classes.txt")
	if err != nil {
		panic(err)
	}
	if _, err := classes.Write([]byte("cat\ndog\n")); err != nil {
		panic(err)
	}

	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("img_%02d", i)
		imgW, err := zw.Create("images/" + name + ".png")
		if err != nil {
			panic(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		c := color.RGBA{R: uint8(i * 20), G: 80, B: 160, A: 255}
		for y := 0; y < 8; y++ {
			for x := 0; x < 8; x++ {
				img.Set(x, y, c)
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			panic(err)
		}
		if _, err := imgW.Write(buf.Bytes()); err != nil {
			panic(err)
		}
		label, err := zw.Create("labels/" + name + ".txt")
		if err != nil {
			panic(err)
		}
		line := fmt.Sprintf("%d 0.50 0.50 0.40 0.40\n", i%2)
		if _, err := label.Write([]byte(line)); err != nil {
			panic(err)
		}
	}
	if err := zw.Close(); err != nil {
		panic(err)
	}
	fmt.Println("wrote", out)
}
