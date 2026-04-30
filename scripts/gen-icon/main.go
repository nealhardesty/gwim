// gen-icon produces the derived icon assets embedded into the GWiM binary.
//
// What it WRITES:
//   - internal/icon/assets/icon-active.ico    (Windows tray; wraps icon-active.png)
//   - internal/icon/assets/icon-suspended.ico (Windows tray; wraps icon-suspended.png)
//   - assets/icon.iconset/*                   (multi-resolution PNGs for iconutil)
//
// What it READS but never writes:
//   - internal/icon/assets/icon-active.png    (hand-drawn, committed source)
//   - internal/icon/assets/icon-suspended.png (hand-drawn, committed source)
//
// The two menu-bar PNGs are tracked source assets. Earlier revisions of
// this script drew them procedurally (a 2x2 grid motif), but the
// project switched to hand-drawn artwork — so this script must NOT
// touch the PNGs even on `make icons`. It only derives the Windows ICO
// container around them.
//
// Run via `make icons` (or `make -f Makefile.windows icons`). The
// script has no external dependencies beyond the Go standard library;
// ICO files are emitted via a hand-rolled encoder (ICONDIR +
// ICONDIRENTRY records wrapping the PNG payload; PNG-in-ICO has been
// supported since Windows Vista which is well below the GWiM minimum).
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

const (
	tintR, tintG = 30, 60
	tintB, tintA = 100, 255
)

// Asset paths. The PNGs are inputs to the ICO encoder; we keep the
// constants here as a single source of truth so the Makefile and this
// script can't drift.
const (
	menuPNGActive    = "internal/icon/assets/icon-active.png"
	menuPNGSuspended = "internal/icon/assets/icon-suspended.png"
	menuICOActive    = "internal/icon/assets/icon-active.ico"
	menuICOSuspended = "internal/icon/assets/icon-suspended.ico"
	iconsetDir       = "assets/icon.iconset"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-icon:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll(iconsetDir, 0o755); err != nil {
		return err
	}

	// Derive Windows ICO files from the hand-drawn menu-bar PNGs. We
	// wrap the PNG payload verbatim in a single-entry ICO container so
	// the ICO matches the exact pixels of the source — Windows tray
	// scales the embedded PNG as needed for whatever DPI bucket is
	// active.
	if err := pngToICO(menuPNGActive, menuICOActive); err != nil {
		return fmt.Errorf("active ICO: %w", err)
	}
	if err := pngToICO(menuPNGSuspended, menuICOSuspended); err != nil {
		return fmt.Errorf("suspended ICO: %w", err)
	}

	// macOS .icns iconset stays procedurally generated — it's a large
	// multi-resolution bundle icon used only when `make app` builds the
	// .app, and the simple 2x2 grid scales cleanly to every required
	// size. If anyone wants the iconset to mirror the hand-drawn
	// menu-bar art too, they'd need to commit pre-rasterised PNGs at
	// each size and route them in here.
	for _, sz := range []int{16, 32, 64, 128, 256, 512, 1024} {
		img := drawBundleIcon(sz)
		if err := writePNG(filepath.Join(iconsetDir, iconsetName(sz, false)), img); err != nil {
			return err
		}
		if sz <= 512 {
			retina := drawBundleIcon(sz * 2)
			if err := writePNG(filepath.Join(iconsetDir, iconsetName(sz, true)), retina); err != nil {
				return err
			}
		}
	}
	return nil
}

// iconsetName builds the macOS-required filename inside an .iconset folder.
// `iconutil --convert icns` expects exactly these names; deviating breaks
// the conversion silently.
func iconsetName(size int, retina bool) string {
	if retina {
		return fmt.Sprintf("icon_%dx%d@2x.png", size, size)
	}
	return fmt.Sprintf("icon_%dx%d.png", size, size)
}

// drawBundleIcon paints the .icns bundle icon — a larger version of
// the same 2x2 grid motif. The Dock will scale these down as needed.
//
// This is the ONLY procedurally-drawn output left after the menu-bar
// PNGs became hand-drawn assets; if the bundle icon ever needs to
// match new artwork, replace this function with hand-drawn input PNGs
// at each iconset size (16, 32, 64, 128, 256, 512, 1024 plus @2x).
func drawBundleIcon(size int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	bg := color.RGBA{245, 245, 250, 255}
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)

	pad := size / 6
	gap := size / 24
	cell := (size - 2*pad - gap) / 2
	fill := color.RGBA{tintR, tintG, tintB, tintA}
	for row := 0; row < 2; row++ {
		for col := 0; col < 2; col++ {
			x0 := pad + col*(cell+gap)
			y0 := pad + row*(cell+gap)
			rect := image.Rect(x0, y0, x0+cell, y0+cell)
			draw.Draw(img, rect, &image.Uniform{C: fill}, image.Point{}, draw.Src)
		}
	}
	return img
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// pngToICO reads the hand-drawn PNG at srcPath and writes a Windows
// ICO file at dstPath that wraps the PNG payload verbatim in a
// single-entry ICONDIR. The file layout is:
//
//	ICONDIR (6 bytes)            — reserved=0, type=1, count=1
//	ICONDIRENTRY (16 bytes)      — width, height, ..., size, offset=22
//	[PNG payload]                — copied byte-for-byte from srcPath
//
// Width / height are uint8 fields; 0 represents 256 per the ICO spec.
// PNG-in-ICO has been supported since Windows Vista so the format is
// compatible with every GWiM target.
func pngToICO(srcPath, dstPath string) error {
	pngBytes, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", srcPath, err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(pngBytes))
	if err != nil {
		return fmt.Errorf("decode %s as PNG: %w", srcPath, err)
	}

	const dirHeaderSize = 6
	const dirEntrySize = 16
	dataOffset := dirHeaderSize + dirEntrySize

	var out bytes.Buffer
	binary.Write(&out, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1)) // type = 1 (icon)
	binary.Write(&out, binary.LittleEndian, uint16(1)) // count = 1

	w := byte(cfg.Width)
	h := byte(cfg.Height)
	if cfg.Width >= 256 {
		w = 0
	}
	if cfg.Height >= 256 {
		h = 0
	}
	out.WriteByte(w)
	out.WriteByte(h)
	out.WriteByte(0)                                               // color count (0 for 32bpp)
	out.WriteByte(0)                                               // reserved
	binary.Write(&out, binary.LittleEndian, uint16(1))             // color planes
	binary.Write(&out, binary.LittleEndian, uint16(32))            // bits per pixel
	binary.Write(&out, binary.LittleEndian, uint32(len(pngBytes))) // bytes in payload
	binary.Write(&out, binary.LittleEndian, uint32(dataOffset))    // payload offset

	out.Write(pngBytes)
	return os.WriteFile(dstPath, out.Bytes(), 0o644)
}
