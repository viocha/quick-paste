//go:build windows

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/png"
	"math"
	"math/bits"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	previewGDI         = windows.NewLazySystemDLL("gdi32.dll")
	procStretchDIBits  = previewGDI.NewProc("StretchDIBits")
	procSetStretchMode = previewGDI.NewProc("SetStretchBltMode")
)

func previewSize(width, height, maxWidth, maxHeight int) (int, int) {
	maxWidth, maxHeight = max(1, min(maxWidth, 2560)), max(1, min(maxHeight, 2560))
	if width <= maxWidth && height <= maxHeight {
		return width, height
	}
	if int64(width)*int64(maxHeight) > int64(height)*int64(maxWidth) {
		return maxWidth, max(1, int(int64(height)*int64(maxWidth)/int64(width)))
	}
	return max(1, int(int64(width)*int64(maxHeight)/int64(height))), maxHeight
}

// 直接将剪贴板 DIB 缩放到预览缓冲区，不为原图额外分配完整解码位图。
func clipboardPreviewPNG(dib []byte, maxWidth, maxHeight int) (string, error) {
	invalid := errors.New("图片格式已损坏")
	if len(dib) < 40 {
		return "", invalid
	}
	headerSize := int(binary.LittleEndian.Uint32(dib[:4]))
	if headerSize < 40 || headerSize > len(dib) {
		return "", invalid
	}
	width, signedHeight := int(int32(binary.LittleEndian.Uint32(dib[4:8]))), int(int32(binary.LittleEndian.Uint32(dib[8:12])))
	height := signedHeight
	if height < 0 {
		height = -height
	}
	if width <= 0 || height <= 0 || width > 1<<20 || height > 1<<20 || binary.LittleEndian.Uint16(dib[12:14]) != 1 {
		return "", invalid
	}
	depth := int(binary.LittleEndian.Uint16(dib[14:16]))
	if depth != 1 && depth != 4 && depth != 8 && depth != 16 && depth != 24 && depth != 32 {
		return "", invalid
	}
	compression := binary.LittleEndian.Uint32(dib[16:20])
	if (compression == 1 && depth != 8) || (compression == 2 && depth != 4) || ((compression == 3 || compression == 6) && depth != 16 && depth != 32) {
		return "", invalid
	}
	colors := int(binary.LittleEndian.Uint32(dib[32:36]))
	if colors == 0 && depth <= 8 {
		colors = 1 << depth
	}
	if colors > 256 || (depth <= 8 && colors > 1<<depth) {
		return "", invalid
	}
	offset := headerSize + colors*4
	if headerSize == 40 {
		if compression == 3 {
			offset += 12
		} else if compression == 6 {
			offset += 16
		}
	}
	if offset >= len(dib) {
		return "", invalid
	}
	if compression == 0 || compression == 3 || compression == 6 {
		stride := (int64(width)*int64(depth) + 31) / 32 * 4
		if stride*int64(height) > int64(len(dib)-offset) {
			return "", invalid
		}
	} else if compression != 1 && compression != 2 {
		return "", errors.New("不支持的图片格式")
	} else if size := binary.LittleEndian.Uint32(dib[20:24]); size == 0 || uint64(size) > uint64(len(dib)-offset) {
		return "", invalid
	}
	w, h := previewSize(width, height, maxWidth, maxHeight)
	if depth == 32 && headerSize >= 56 && binary.LittleEndian.Uint32(dib[52:56]) != 0 {
		return encodeClipboardPreview(scaleAlphaDIB(dib, offset, width, signedHeight, w, h))
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return "", errors.New("创建图片预览失败")
	}
	defer procDeleteDC.Call(dc)
	info := bitmapInfo{Header: bitmapInfoHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}}
	var bits uintptr
	bitmap, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&info)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == 0 {
		return "", errors.New("创建图片预览失败")
	}
	defer procDeleteObject.Call(bitmap)
	previous, _, _ := procSelectObject.Call(dc, bitmap)
	if previous == 0 {
		return "", errors.New("创建图片预览失败")
	}
	defer procSelectObject.Call(dc, previous)
	procSetStretchMode.Call(dc, 4) // HALFTONE
	lines, _, _ := procStretchDIBits.Call(dc, 0, 0, uintptr(w), uintptr(h), 0, 0, uintptr(width), uintptr(height), uintptr(unsafe.Pointer(&dib[offset])), uintptr(unsafe.Pointer(&dib[0])), 0, 0x00CC0020)
	runtime.KeepAlive(dib)
	if lines == 0 || int32(lines) == -1 {
		return "", errors.New("缩放图片预览失败")
	}
	raw := unsafe.Slice((*byte)(unsafe.Pointer(bits)), w*h*4)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(raw); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = raw[i+2], raw[i+1], raw[i], 255
	}
	return encodeClipboardPreview(img)
}

// GDI 缩放不保留 alpha；透明 DIB 从源像素直接采样，按预乘颜色插值避免透明边缘色晕。
func scaleAlphaDIB(dib []byte, offset, width, signedHeight, w, h int) *image.NRGBA {
	height := signedHeight
	if height < 0 {
		height = -height
	}
	var masks [4]uint32
	var shifts [4]int
	for i := range masks {
		masks[i] = binary.LittleEndian.Uint32(dib[40+i*4:])
		if masks[i] == 0 && i < 3 {
			masks[i] = []uint32{0x00ff0000, 0x0000ff00, 0x000000ff}[i]
		}
		shifts[i] = bits.TrailingZeros32(masks[i])
	}
	standardMasks := masks == [4]uint32{0x00ff0000, 0x0000ff00, 0x000000ff, 0xff000000}
	pixel := func(x, y int) [4]float64 {
		if signedHeight > 0 {
			y = height - 1 - y
		}
		at := offset + (y*width+x)*4
		var c [4]float64
		if standardMasks {
			c = [4]float64{float64(dib[at+2]), float64(dib[at+1]), float64(dib[at]), float64(dib[at+3])}
		} else {
			v := binary.LittleEndian.Uint32(dib[at:])
			for i, mask := range masks {
				c[i] = float64((v&mask)>>shifts[i]) * 255 / float64(mask>>shifts[i])
			}
		}
		alphaScale := c[3] * (1.0 / 255)
		for i := 0; i < 3; i++ {
			c[i] *= alphaScale
		}
		return c
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	scaleX, scaleY := float64(width)/float64(w), float64(height)/float64(h)
	for y := 0; y < h; y++ {
		sy := max(0, (float64(y)+0.5)*scaleY-0.5)
		y0 := int(sy)
		fy := sy - float64(y0)
		for x := 0; x < w; x++ {
			sx := max(0, (float64(x)+0.5)*scaleX-0.5)
			x0 := int(sx)
			fx := sx - float64(x0)
			p := [4][4]float64{pixel(x0, y0), pixel(min(x0+1, width-1), y0), pixel(x0, min(y0+1, height-1)), pixel(min(x0+1, width-1), min(y0+1, height-1))}
			weights := [4]float64{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy}
			var c [4]float64
			for i := range p {
				for channel := range c {
					c[channel] += p[i][channel] * weights[i]
				}
			}
			if c[3] > 0 {
				for channel := 0; channel < 3; channel++ {
					c[channel] *= 255 / c[3]
				}
			}
			for channel := range c {
				img.Pix[(y*w+x)*4+channel] = byte(min(255, max(0, math.Round(c[channel]))))
			}
		}
	}
	return img
}

func encodeClipboardPreview(img image.Image) (string, error) {
	var output bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&output, img); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(output.Bytes()), nil
}
