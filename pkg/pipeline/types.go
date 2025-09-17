package pipeline

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/rwcarlsen/goexif/exif"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/carapace/pkg/validate"
	"github.com/tdeslauriers/pixie/pkg/api"

	redraw "golang.org/x/image/draw"
)

const (
	JpegQuality  int = 85
	BlurLongSide int = 32 // long side in pixels for blur/placeholder image
)

// ParseObjectKey is a helper which parses the object key from the webhook
// to extract the directory, file name, file extension, and slug.
// Exists to abstract away this logic from the main processing loop.
func ParseObjectKey(objectKey string) (dir, file, ext, slug string, err error) {

	// validate object key
	if objectKey == "" {
		return "", "", "", "", fmt.Errorf("object key is empty")
	}

	// get the directory from the object key
	dir = filepath.Dir(objectKey)

	// drop the bucket name from the directory if it exists
	if strings.Contains(dir, "/") {
		parts := strings.SplitN(dir, "/", 2)
		dir = parts[1]
	}

	// get the file name from the object key
	file = filepath.Base(objectKey)
	if file == "" {
		return "", "", "", "", fmt.Errorf("file name is empty in object key: %s", objectKey)
	}

	// get the file extension from the file name
	ext = filepath.Ext(file)
	if ext == "" || !api.IsValidExtension(ext) {
		return "", "", "", "", fmt.Errorf("file extension must not be empty and must be a valid file type: %s", objectKey)
	}

	// get the slug from the file name
	slug = strings.TrimSuffix(file, ext)
	if slug == "" || !validate.IsValidUuid(slug) {
		return "", "", "", "", fmt.Errorf("invalid slug in object key: %s", objectKey)
	}

	return dir, file, ext, slug, nil
}

// Exif represents a subset of the EXIF metadata extracted from an image/picture.
type Exif struct {
	// best effort -> tries DateTimeOriginal, DateTimeDigitized, DateTime.
	TakenAt *time.Time `json:"taken_at,omitempty"`

	// pixel dimensions
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// rotation in degrees
	// 0, 90, 180, 270
	// 0 means no rotation
	Rotation int `json:"rotation,omitempty"`

	// optional GPS coordinates -> not often present in images
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
}

// ReadExif is a function type that defines the signature for reading
// EXIF metadata from ReadSeekCloser stream.
// If not exif data is found, returns an empty Exif struct and no error.
func ReadExif(r storage.ReadSeekCloser) (*Exif, error) {

	meta := &Exif{}

	// not all images have exif data, jpeg and tiff are the most common
	// png and gif typically do not have exif data
	// if no exif data, return empty meta struct and no error
	if x, err := exif.Decode(r); err == nil && x != nil {

		// get the best effort date time -> DateTimeOriginal, DateTimeDigitized, DateTime
		if datetime, err := x.DateTime(); err == nil {
			// set the taken at time
			meta.TakenAt = &datetime
		}

		// get the orientationa and convert to rotation in degrees
		if orient, ok := tagToInt(exif.Orientation, x); ok {
			meta.Rotation = convertToDegrees(orient)
		}

		// gps coordinates will not be present in most images
		if lat, lon, err := x.LatLong(); err == nil {
			// set the gps coordinates
			meta.Latitude = lat
			meta.Longitude = lon
		}

		// get the pixel dimensions
		if width, ok := tagToInt(exif.PixelXDimension, x); ok {
			meta.Width = width
		}

		if height, ok := tagToInt(exif.PixelYDimension, x); ok {
			meta.Height = height
		}

		// get the orientation and convert to rotation in degrees
		if orientation, ok := tagToInt(exif.Orientation, x); ok {
			meta.Rotation = convertToDegrees(orientation)
		}
	}

	// check if exif data was found -> we can get width and height from image config
	if meta.Width == 0 || meta.Height == 0 {

		// rewind the reader to read the image config
		_, _ = r.Seek(0, io.SeekStart)

		// decode image config to get width and height
		if config, _, err := image.DecodeConfig(r); err == nil {
			meta.Width = config.Width
			meta.Height = config.Height
		}

	}

	// rewind the reader to read the image config
	_, _ = r.Seek(0, io.SeekStart)

	return meta, nil
}

// tagToInt is a helper to convert exif tag strings to ints
func tagToInt(tag exif.FieldName, x *exif.Exif) (int, bool) {

	if t, err := x.Get(tag); err == nil && t != nil {

		if i, err := t.Int(0); err == nil {
			return i, true
		}

		if num, den, err := t.Rat2(0); err == nil && den != 0 {
			return int(num / den), true
		}
	}

	return 0, false
}

// convertToDegrees converts EXIF orientation values to rotation in degrees.
func convertToDegrees(orientation int) int {
	// exif orientation -> rotation (clockwise).
	// mirror cases map to equivalent rotations here.
	switch orientation {
	case 1: // normal
		return 0
	case 2: // mirror horizontal
		return 0
	case 3: // rotate 180
		return 180
	case 4: // mirror vertical
		return 180
	case 5: // mirror horizontal + rotate 270 clockwise
		return 270
	case 6: // rotate 90 clockwise
		return 90
	case 7: // mirror horizontal + rotate 90 clockwise
		return 90
	case 8: // rotate 270 clockwise
		return 270
	default:
		return 0
	}
}

// rotateImage rotates an image based on the provided rotation in degrees.
func rotateImage(src image.Image, degrees int) image.Image {
	switch ((degrees % 360) + 360) % 360 { // normalize degrees to [0, 360) -> accounts for negative degrees
	case 0:
		return src // no rotation needed
	case 90:
		return rotate90(src)
	case 180:
		return rotate180(src)
	case 270:
		return rotate270(src)
	default:
		return src // unsupported rotation, return original
	}
}

// rotate90 is a helper function to rotate an image 90 degrees clockwise.
func rotate90(src image.Image) image.Image {

	// get image bounds
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(h-1-y, x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}

	return dst
}

// rotate180 is a helper function to rotate an image 180 degrees.
func rotate180(src image.Image) image.Image {

	// get image bounds
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(w-1-x, h-1-y, src.At(bounds.Min.X+x, bounds.Min.Y+y))
		}
	}

	return dst
}

// rotate270 is a helper function to rotate an image 270 degrees clockwise.
func rotate270(src image.Image) image.Image {

	// get image bounds
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()

	dst := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dst.Set(y, w-1-x, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}

	return dst
}

// resizeImageToWidth is a helper method which resizes the provided image to the
// specified width while maintaining aspect ratio amd returns the resized image.
func resizeImageToWidth(src image.Image, width int) image.Image {

	// validate width
	if width <= 0 {
		return src // return original image if invalid width
	}

	// get original dimensions
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return src // return original image if invalid dimensions
	}

	// validate resizing is necessary
	if w <= width {
		return src // return original image if already smaller than target width
	}

	// calculate the new width and height to maintain aspect ratio
	scale := float64(width) / float64(w)
	dstWidth := width
	dstHeight := int(math.Round(float64(h) * scale))

	// create a new image with the new dimensions
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	redraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, redraw.Over, nil)

	return dst
}

// encodeToJpeg is a helper method which encodes the provided image to JPEG format
// with the specified quality and returns the encoded bytes.
func encodeToJpeg(src image.Image, quality int) ([]byte, error) {

	// validate quality
	if quality < 1 || quality > 100 {
		quality = JpegQuality // set to default if invalid
	}

	// check if image has an alpha channel
	if hasAlphaChannel(src) {
		// flatten the image on a white background to remove transparency
		src = flattenOnWhite(src)
	}

	// encode the image to JPEG format
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: clamp(quality, 1, 100)}); err != nil {
		return nil, fmt.Errorf("failed to encode image to JPEG: %v", err)
	}

	return buf.Bytes(), nil

}

// hasAlphaChannel checks if the provided image has an alpha channel
func hasAlphaChannel(img image.Image) bool {
	switch img.(type) {
	case *image.NRGBA, *image.NRGBA64, *image.RGBA, *image.RGBA64, *image.Alpha, *image.Alpha16:
		return true
	default:
		// treat images without the above as not having an alpha channel by default
		return false
	}
}

// flattenOnWhite flattens an image with an alpha channel onto a white background, ie,
// it removes transparency by compositing the image over a white canvas.
func flattenOnWhite(src image.Image) image.Image {

	// get image bounds
	bounds := src.Bounds()

	dst := image.NewRGBA(bounds)

	// fill white into the destination image
	draw.Draw(dst, bounds, &image.Uniform{C: image.White}, image.Point{}, draw.Src)

	// composite the source image over the white background
	draw.Draw(dst, bounds, src, bounds.Min, draw.Over)

	return dst
}

// clamp is a helper function which ensures a value is within the min and max bounds.
func clamp(v, min, max int) int {

	if v < min {
		return min
	}
	if v > max {
		return max
	}

	return v
}

// resizeToLongestSide resizes an image to fit within the specified longest side length,
// maintaining the aspect ratio. If the image is already smaller than the target size,
// it returns the original image.
func resizeToLongestSide(src image.Image) image.Image {

	// get original dimensions
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return src // return original image if invalid dimensions
	}

	// determine the longest side
	longest := w
	if h > w {
		longest = h
	}

	// validate resizing is necessary
	if longest <= BlurLongSide {
		return src // return original image if already smaller than target longest side
	}

	// calculate the new width and height to maintain aspect ratio
	scale := float64(BlurLongSide) / float64(longest)
	dstWidth := int(math.Round(float64(w) * scale))
	dstHeight := int(math.Round(float64(h) * scale))

	// create a new image with the new dimensions
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	redraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, redraw.Over, nil)

	return dst
}
