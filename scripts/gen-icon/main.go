// gen-icon generates the menu-bar icons embedded into the GWiM binary.
//
// It produces:
//   - internal/icon/assets/icon-active.png    (filled grid "G")
//   - internal/icon/assets/icon-suspended.png (outlined grid "G")
//   - assets/icon.iconset/*                   (multi-resolution PNGs for iconutil)
//
// Run via `make icons`. The script has no external dependencies — it draws
// directly with image/draw and image/png from the standard library so the
// build works on a vanilla Go installation.
package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

const (
	menuIconSize     = 22 // points; matches NSStatusItem default
	menuIconScale    = 2  // @2x retina
	bundleIconSize   = 1024
	tintR, tintG     = 30, 60
	tintB, tintA     = 100, 255
	dimR, dimG, dimB = 120, 120, 120
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-icon:", err)
		os.Exit(1)
	}
}

func run() error {
	if err := os.MkdirAll("internal/icon/assets", 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll("assets/icon.iconset", 0o755); err != nil {
		return err
	}

	active := drawMenuBar(true)
	suspended := drawMenuBar(false)
	if err := writePNG("internal/icon/assets/icon-active.png", active); err != nil {
		return err
	}
	if err := writePNG("internal/icon/assets/icon-suspended.png", suspended); err != nil {
		return err
	}

	for _, sz := range []int{16, 32, 64, 128, 256, 512, 1024} {
		img := drawBundleIcon(sz)
		if err := writePNG(filepath.Join("assets/icon.iconset", iconsetName(sz, false)), img); err != nil {
			return err
		}
		if sz <= 512 {
			retina := drawBundleIcon(sz * 2)
			if err := writePNG(filepath.Join("assets/icon.iconset", iconsetName(sz, true)), retina); err != nil {
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

// drawMenuBar draws the small status-bar icon. We use a 2x2 grid of squares
// (a stylised "G" / "window" motif) so users can tell GWiM apart in a busy
// menu bar even at small size.
//
// Active variant is a solid tint; suspended variant is outline-only.
func drawMenuBar(active bool) image.Image {
	w := menuIconSize * menuIconScale
	h := menuIconSize * menuIconScale
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	pad := w / 6
	gap := 2 * menuIconScale
	cell := (w - 2*pad - gap) / 2

	var fill color.RGBA
	if active {
		fill = color.RGBA{tintR, tintG, tintB, tintA}
	} else {
		fill = color.RGBA{dimR, dimG, dimB, 255}
	}

	for row := 0; row < 2; row++ {
		for col := 0; col < 2; col++ {
			x0 := pad + col*(cell+gap)
			y0 := pad + row*(cell+gap)
			rect := image.Rect(x0, y0, x0+cell, y0+cell)
			if active {
				draw.Draw(img, rect, &image.Uniform{C: fill}, image.Point{}, draw.Src)
			} else {
				strokeRect(img, rect, fill, menuIconScale)
			}
		}
	}
	return img
}

// drawBundleIcon paints a larger version of the same motif for the .icns
// bundle icon. The Dock will scale these down as needed.
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

// strokeRect draws a hollow rectangle of the given thickness onto img.
func strokeRect(img *image.RGBA, r image.Rectangle, c color.RGBA, thickness int) {
	if thickness < 1 {
		thickness = 1
	}
	top := image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+thickness)
	bot := image.Rect(r.Min.X, r.Max.Y-thickness, r.Max.X, r.Max.Y)
	left := image.Rect(r.Min.X, r.Min.Y, r.Min.X+thickness, r.Max.Y)
	right := image.Rect(r.Max.X-thickness, r.Min.Y, r.Max.X, r.Max.Y)
	src := &image.Uniform{C: c}
	draw.Draw(img, top, src, image.Point{}, draw.Src)
	draw.Draw(img, bot, src, image.Point{}, draw.Src)
	draw.Draw(img, left, src, image.Point{}, draw.Src)
	draw.Draw(img, right, src, image.Point{}, draw.Src)
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
