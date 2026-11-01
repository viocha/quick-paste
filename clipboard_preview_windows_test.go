//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

func previewDIB(width, height, depth int, alpha bool) []byte {
	header := 40
	if alpha {
		header = 124
	}
	h := height
	if h < 0 {
		h = -h
	}
	stride := (width*depth + 31) / 32 * 4
	dib := make([]byte, header+stride*h)
	binary.LittleEndian.PutUint32(dib[:4], uint32(header))
	binary.LittleEndian.PutUint32(dib[4:8], uint32(width))
	binary.LittleEndian.PutUint32(dib[8:12], uint32(height))
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], uint16(depth))
	if alpha {
		binary.LittleEndian.PutUint32(dib[16:20], 3)
		for i, mask := range []uint32{0x00ff0000, 0x0000ff00, 0x000000ff, 0xff000000} {
			binary.LittleEndian.PutUint32(dib[40+i*4:], mask)
		}
	}
	for y := 0; y < h; y++ {
		row := y
		if height > 0 {
			row = h - 1 - y
		}
		for x := 0; x < width; x++ {
			i := header + row*stride + x*(depth/8)
			dib[i], dib[i+1], dib[i+2] = 20, byte(y*200/max(1, h-1)), 240
			if depth == 32 && alpha {
				dib[i+3] = 128
			}
		}
	}
	return dib
}

func TestClipboardPreviewPNG(t *testing.T) {
	for _, tc := range []struct {
		name                                                             string
		width, height, depth, maxWidth, maxHeight, wantWidth, wantHeight int
		alpha                                                            bool
	}{
		{"24位行补齐", 3, 2, 24, 20, 20, 3, 2, false},
		{"自顶向下", 3, -2, 24, 20, 20, 3, 2, false},
		{"32位无透明度", 10, 8, 32, 5, 5, 5, 4, false},
		{"V5透明度", 10, -8, 32, 5, 5, 5, 4, true},
		{"横图", 4000, 3000, 32, 800, 240, 320, 240, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, err := clipboardPreviewPNG(previewDIB(tc.width, tc.height, tc.depth, tc.alpha), tc.maxWidth, tc.maxHeight)
			if err != nil {
				t.Fatal(err)
			}
			data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != tc.wantWidth || img.Bounds().Dy() != tc.wantHeight {
				t.Fatalf("预览尺寸 %v", img.Bounds())
			}
			pixel := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
			wantAlpha := byte(255)
			if tc.alpha {
				wantAlpha = 128
			}
			if pixel.A != wantAlpha || pixel.R < 235 || pixel.B > 25 {
				t.Fatalf("颜色或透明度错误: %+v", pixel)
			}
			if tc.wantHeight == 2 {
				bottom := color.NRGBAModel.Convert(img.At(0, 1)).(color.NRGBA)
				if pixel.G != 0 || bottom.G != 200 {
					t.Fatalf("行方向错误: %+v %+v", pixel, bottom)
				}
			}
			if tc.width == 4000 && len(url) > 50000 {
				t.Fatalf("缩略图数据过大: %d", len(url))
			}
		})
	}
}

func TestClipboardPreviewRejectsInvalidDIB(t *testing.T) {
	valid := previewDIB(3, 2, 24, false)
	for _, dib := range [][]byte{nil, valid[:39], valid[:len(valid)-1], make([]byte, 40)} {
		if _, err := clipboardPreviewPNG(dib, 800, 240); err == nil {
			t.Fatal("应拒绝损坏的 DIB")
		}
	}
}

func TestClipboardPreviewKeepsOriginalPayload(t *testing.T) {
	store := testClipboardStore(t)
	dib := previewDIB(1024, 768, 32, true)
	item, err := store.Add(ClipboardItem{Kind: ClipboardImage, Preview: "图片"}, map[uint32][]byte{cfDIBV5: dib})
	if err != nil {
		t.Fatal(err)
	}
	service := &ClipboardService{store: store}
	url, err := service.GetPreviewData(item.ID, 640, 240)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatal("应返回 PNG 缩略图")
	}
	_, payload, err := store.Payload(item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload[cfDIBV5], dib) {
		t.Fatal("生成预览不得修改原始剪贴板数据")
	}
}

func TestClipboardPreviewAlphaInterpolation(t *testing.T) {
	dib := previewDIB(2, -1, 32, true)
	copy(dib[124:], []byte{0, 0, 255, 255, 255, 0, 0, 0})
	url, err := clipboardPreviewPNG(dib, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	pixel := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
	if pixel != (color.NRGBA{R: 255, A: 128}) {
		t.Fatalf("透明插值出现色晕: %+v", pixel)
	}
}

func TestClipboardPreviewPaletteAndBitfields(t *testing.T) {
	for _, depth := range []int{8, 16} {
		extra := 8
		if depth == 16 {
			extra = 12
		}
		dib := make([]byte, 40+extra+4)
		binary.LittleEndian.PutUint32(dib[:4], 40)
		binary.LittleEndian.PutUint32(dib[4:8], 2)
		binary.LittleEndian.PutUint32(dib[8:12], 1)
		binary.LittleEndian.PutUint16(dib[12:14], 1)
		binary.LittleEndian.PutUint16(dib[14:16], uint16(depth))
		if depth == 8 {
			binary.LittleEndian.PutUint32(dib[32:36], 2)
			copy(dib[40:], []byte{0, 0, 255, 0, 255, 0, 0, 0, 0, 1, 0, 0})
		} else {
			binary.LittleEndian.PutUint32(dib[16:20], 3)
			for i, mask := range []uint32{0xf800, 0x07e0, 0x001f} {
				binary.LittleEndian.PutUint32(dib[40+i*4:], mask)
			}
			copy(dib[52:], []byte{0, 0xf8, 0x1f, 0})
		}
		url, err := clipboardPreviewPNG(dib, 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		red := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA)
		blue := color.NRGBAModel.Convert(img.At(1, 0)).(color.NRGBA)
		if red.R < 248 || red.B != 0 || blue.R != 0 || blue.B < 248 {
			t.Fatalf("%d 位颜色错误: %+v %+v", depth, red, blue)
		}
	}
}

func TestClipboardPreviewNonstandardAlphaMasks(t *testing.T) {
	dib := previewDIB(1, -1, 32, true)
	binary.LittleEndian.PutUint32(dib[40:44], 0xff)
	binary.LittleEndian.PutUint32(dib[48:52], 0xff0000)
	copy(dib[124:], []byte{255, 0, 0, 128})
	url, err := clipboardPreviewPNG(dib, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(url, "data:image/png;base64,"))
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if pixel := color.NRGBAModel.Convert(img.At(0, 0)).(color.NRGBA); pixel != (color.NRGBA{R: 255, A: 128}) {
		t.Fatalf("位域颜色错误: %+v", pixel)
	}
}

func BenchmarkClipboardPreviewLargeImage(b *testing.B) {
	dib := previewDIB(4000, 3000, 32, false)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := clipboardPreviewPNG(dib, 900, 240); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClipboardPreviewDetailAlpha(b *testing.B) {
	dib := previewDIB(4000, 3000, 32, true)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := clipboardPreviewPNG(dib, 1600, 1280); err != nil {
			b.Fatal(err)
		}
	}
}
