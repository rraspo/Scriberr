package api

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"

	"scriberr/internal/models"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const (
	sheetColumns    = 4
	sheetRows       = 3
	sheetTileWidth  = 400
	sheetFrameCount = sheetColumns * sheetRows
	sheetJPEGQual   = 85
)

// formatSheetTimestamp renders a frame time as m:ss (h:mm:ss from one hour),
// with a tenth only when the time is not a whole second.
func formatSheetTimestamp(seconds float64) string {
	tenths := int(math.Round(seconds * 10))
	whole := tenths / 10
	fraction := ""
	if tenths%10 != 0 {
		fraction = fmt.Sprintf(".%d", tenths%10)
	}
	hours := whole / 3600
	minutes := (whole % 3600) / 60
	secs := whole % 60
	if hours > 0 {
		return fmt.Sprintf("%d:%02d:%02d%s", hours, minutes, secs, fraction)
	}
	return fmt.Sprintf("%d:%02d%s", minutes, secs, fraction)
}

// buildContactSheets tiles the frames, twelve to a sheet (four columns by
// three rows), each tile stamped with its timestamp, and writes the sheets
// as <sheetDir>/<index>.jpg.
func buildContactSheets(frameDir, sheetDir string, frames []models.JobFrame, jobID string) ([]models.JobSheet, error) {
	if len(frames) == 0 {
		return nil, fmt.Errorf("no frames to tile")
	}
	if err := os.MkdirAll(sheetDir, 0o755); err != nil {
		return nil, err
	}
	first, err := decodeJPEGFile(filepath.Join(frameDir, fmt.Sprintf("%d.jpg", frames[0].Index)))
	if err != nil {
		return nil, err
	}
	bounds := first.Bounds()
	tileHeight := int(math.Round(float64(sheetTileWidth) * float64(bounds.Dy()) / float64(bounds.Dx())))

	var sheets []models.JobSheet
	for start := 0; start < len(frames); start += sheetFrameCount {
		end := start + sheetFrameCount
		if end > len(frames) {
			end = len(frames)
		}
		group := frames[start:end]
		canvas := image.NewRGBA(image.Rect(0, 0, sheetColumns*sheetTileWidth, sheetRows*tileHeight))
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
		for position, frame := range group {
			source, err := decodeJPEGFile(filepath.Join(frameDir, fmt.Sprintf("%d.jpg", frame.Index)))
			if err != nil {
				return nil, err
			}
			tile := image.Rect(
				(position%sheetColumns)*sheetTileWidth,
				(position/sheetColumns)*tileHeight,
				(position%sheetColumns+1)*sheetTileWidth,
				(position/sheetColumns+1)*tileHeight,
			)
			drawFittedTile(canvas, tile, source)
			stampTimestamp(canvas, tile, formatSheetTimestamp(frame.TimestampSeconds))
		}

		index := len(sheets)
		path := filepath.Join(sheetDir, fmt.Sprintf("%d.jpg", index))
		if err := writeJPEGFile(path, canvas); err != nil {
			return nil, err
		}
		sheets = append(sheets, models.JobSheet{
			Index:        index,
			StartSeconds: group[0].TimestampSeconds,
			EndSeconds:   group[len(group)-1].TimestampSeconds,
			FrameCount:   len(group),
			URL:          fmt.Sprintf("/api/v1/transcription/%s/sheets/%d", jobID, index),
			ContentType:  "image/jpeg",
		})
	}
	return sheets, nil
}

func decodeJPEGFile(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return jpeg.Decode(file)
}

func writeJPEGFile(path string, picture image.Image) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := jpeg.Encode(file, picture, &jpeg.Options{Quality: sheetJPEGQual}); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

// drawFittedTile scales the frame to fit the tile, centered on black.
func drawFittedTile(canvas *image.RGBA, tile image.Rectangle, source image.Image) {
	bounds := source.Bounds()
	scale := math.Min(float64(tile.Dx())/float64(bounds.Dx()), float64(tile.Dy())/float64(bounds.Dy()))
	width := int(math.Round(float64(bounds.Dx()) * scale))
	height := int(math.Round(float64(bounds.Dy()) * scale))
	left := tile.Min.X + (tile.Dx()-width)/2
	top := tile.Min.Y + (tile.Dy()-height)/2
	xdraw.CatmullRom.Scale(canvas, image.Rect(left, top, left+width, top+height), source, bounds, xdraw.Over, nil)
}

// stampTimestamp writes the text in white, doubled in size, on a
// semi-opaque dark box in the tile's bottom-left corner.
func stampTimestamp(canvas *image.RGBA, tile image.Rectangle, text string) {
	face := basicfont.Face7x13
	textWidth := font.MeasureString(face, text).Ceil()
	label := image.NewRGBA(image.Rect(0, 0, textWidth+6, 17))
	draw.Draw(label, label.Bounds(), image.NewUniform(color.RGBA{A: 175}), image.Point{}, draw.Src)
	drawer := font.Drawer{
		Dst:  label,
		Src:  image.NewUniform(color.White),
		Face: face,
		Dot:  fixed.P(3, 13),
	}
	drawer.DrawString(text)

	scaled := image.Rect(0, 0, label.Bounds().Dx()*2, label.Bounds().Dy()*2)
	target := scaled.Add(image.Pt(tile.Min.X+4, tile.Max.Y-scaled.Dy()-4))
	xdraw.NearestNeighbor.Scale(canvas, target, label, label.Bounds(), xdraw.Over, nil)
}
