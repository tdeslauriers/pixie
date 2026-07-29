package pipeline

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"sync"
	"testing"

	"github.com/tdeslauriers/carapace/pkg/storage"
)

// testUUID/testUUID2 are fixed, deterministic UUID-shaped strings (they only
// need to satisfy validate.ValidateUuid's generic hex-and-dashes pattern) used
// as fixtures across the pipeline package's tests.
const (
	testUUID  = "11111111-1111-1111-1111-111111111111"
	testUUID2 = "22222222-2222-2222-2222-222222222222"
)

func TestParseObjectKey(t *testing.T) {

	tests := []struct {
		name     string
		key      string
		wantDir  string
		wantFile string
		wantExt  string
		wantSlug string
		wantErr  bool
	}{
		{
			name:     "bucket-prefixed uploads key",
			key:      "gallery-images/uploads/" + testUUID + ".jpg",
			wantDir:  "uploads",
			wantFile: testUUID + ".jpg",
			wantExt:  ".jpg",
			wantSlug: testUUID,
		},
		{
			name:     "bucket-prefixed year directory key",
			key:      "gallery-images/2024/" + testUUID + ".png",
			wantDir:  "2024",
			wantFile: testUUID + ".png",
			wantExt:  ".png",
			wantSlug: testUUID,
		},
		{
			name:     "deeper nested path keeps everything after the bucket component",
			key:      "gallery-images/2024/sub/" + testUUID + ".jpg",
			wantDir:  "2024/sub",
			wantFile: testUUID + ".jpg",
			wantExt:  ".jpg",
			wantSlug: testUUID,
		},
		{
			name:     "no directory at all",
			key:      testUUID + ".jpg",
			wantDir:  ".",
			wantFile: testUUID + ".jpg",
			wantExt:  ".jpg",
			wantSlug: testUUID,
		},
		{
			name:    "empty key is an error",
			key:     "",
			wantErr: true,
		},
		{
			name:    "missing extension is an error",
			key:     "gallery-images/uploads/" + testUUID,
			wantErr: true,
		},
		{
			name:    "disallowed extension is an error",
			key:     "gallery-images/uploads/" + testUUID + ".txt",
			wantErr: true,
		},
		{
			name:    "non-uuid slug is an error",
			key:     "gallery-images/uploads/not-a-uuid.jpg",
			wantErr: true,
		},
		{
			name:    "trailing slash yields empty file name",
			key:     "gallery-images/uploads/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, file, ext, slug, err := ParseObjectKey(tt.key)

			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseObjectKey(%q) error = %v, wantErr %v", tt.key, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}

			if dir != tt.wantDir {
				t.Errorf("dir = %q, want %q", dir, tt.wantDir)
			}
			if file != tt.wantFile {
				t.Errorf("file = %q, want %q", file, tt.wantFile)
			}
			if ext != tt.wantExt {
				t.Errorf("ext = %q, want %q", ext, tt.wantExt)
			}
			if slug != tt.wantSlug {
				t.Errorf("slug = %q, want %q", slug, tt.wantSlug)
			}
		})
	}
}

// encodeJpeg is a test helper producing a valid, EXIF-free JPEG of the given
// dimensions filled with a solid color.
func encodeJpeg(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("failed to encode fixture jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestReadExif(t *testing.T) {

	t.Run("image with no exif data returns dimensions but no date/rotation", func(t *testing.T) {
		data := encodeJpeg(t, 64, 32, color.White)

		meta, err := ReadExif(newFakeReadSeekCloser(data))
		if err != nil {
			t.Fatalf("ReadExif() unexpected error: %v", err)
		}
		if meta.TakenAt != nil {
			t.Errorf("TakenAt = %v, want nil (no exif data present)", meta.TakenAt)
		}
		if meta.Rotation != 0 {
			t.Errorf("Rotation = %d, want 0", meta.Rotation)
		}
		if meta.Width != 64 || meta.Height != 32 {
			t.Errorf("dimensions = %dx%d, want 64x32", meta.Width, meta.Height)
		}
		if meta.Latitude != nil || meta.Longitude != nil {
			t.Errorf("expected no GPS data, got lat=%v lon=%v", meta.Latitude, meta.Longitude)
		}
	})

	t.Run("undecodable bytes still returns an empty, zero-value meta with no error", func(t *testing.T) {
		meta, err := ReadExif(newFakeReadSeekCloser([]byte("not an image")))
		if err != nil {
			t.Fatalf("ReadExif() unexpected error: %v", err)
		}
		if meta.TakenAt != nil || meta.Width != 0 || meta.Height != 0 {
			t.Errorf("expected zero-value meta, got %+v", meta)
		}
	})

	t.Run("seek failure is propagated as an error", func(t *testing.T) {
		_, err := ReadExif(seekErrReadSeekCloser{})
		if err == nil {
			t.Fatal("expected error when rewinding the reader fails, got nil")
		}
	})
}

func TestConvertToDegrees(t *testing.T) {

	tests := []struct {
		name        string
		orientation int
		want        int
	}{
		{"1 normal", 1, 0},
		{"2 mirror horizontal", 2, 0},
		{"3 rotate 180", 3, 180},
		{"4 mirror vertical", 4, 180},
		{"5 mirror + rotate 270", 5, 270},
		{"6 rotate 90 clockwise", 6, 90},
		{"7 mirror + rotate 90", 7, 90},
		{"8 rotate 270 clockwise", 8, 270},
		{"0 unknown/absent", 0, 0},
		{"9 out of range", 9, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := convertToDegrees(tt.orientation); got != tt.want {
				t.Errorf("convertToDegrees(%d) = %d, want %d", tt.orientation, got, tt.want)
			}
		})
	}
}

// asymmetric3x2 builds a 3(w)x2(h) RGBA image with a distinct color in every
// pixel so rotations/reflections can be verified precisely by position.
func asymmetric3x2() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 3, 2))
	colors := []color.RGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255},
		{255, 255, 0, 255}, {0, 255, 255, 255}, {255, 0, 255, 255},
	}
	i := 0
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			img.Set(x, y, colors[i])
			i++
		}
	}
	return img
}

func TestRotateImage(t *testing.T) {

	src := asymmetric3x2() // 3 wide x 2 tall

	tests := []struct {
		name        string
		degrees     int
		wantW       int
		wantH       int
		wantAt      image.Point // pixel to sample
		wantColorAt color.Color // expected color at wantAt, sourced from src.At(0,0)
	}{
		{"0 degrees is a no-op", 0, 3, 2, image.Pt(0, 0), src.At(0, 0)},
		{"360 normalizes to 0", 360, 3, 2, image.Pt(0, 0), src.At(0, 0)},
		{"negative normalizes into range (-90 -> 270)", -90, 2, 3, image.Pt(0, 2), src.At(0, 0)},
		{"90 clockwise swaps dimensions", 90, 2, 3, image.Pt(1, 0), src.At(0, 0)},
		{"180 flips both axes", 180, 3, 2, image.Pt(2, 1), src.At(0, 0)},
		{"270 clockwise swaps dimensions", 270, 2, 3, image.Pt(0, 2), src.At(0, 0)},
		{"unsupported degree value returns original", 45, 3, 2, image.Pt(0, 0), src.At(0, 0)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rotateImage(src, tt.degrees)

			b := got.Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Fatalf("rotated dims = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}

			r1, g1, b1, a1 := got.At(tt.wantAt.X, tt.wantAt.Y).RGBA()
			r2, g2, b2, a2 := tt.wantColorAt.RGBA()
			if r1 != r2 || g1 != g2 || b1 != b2 || a1 != a2 {
				t.Errorf("pixel at %v = %v, want %v (src's origin pixel)", tt.wantAt, got.At(tt.wantAt.X, tt.wantAt.Y), tt.wantColorAt)
			}
		})
	}
}

func TestResizeImageToWidth(t *testing.T) {

	tests := []struct {
		name      string
		srcW      int
		srcH      int
		width     int
		wantW     int
		wantH     int
		wantSameW bool // true if we expect the function to hand back the source unresized
	}{
		{"invalid zero width returns original", 100, 50, 0, 100, 50, true},
		{"negative width returns original", 100, 50, -5, 100, 50, true},
		{"target already >= source width returns original", 100, 50, 200, 100, 50, true},
		{"downscale maintains aspect ratio", 200, 100, 100, 100, 50, false},
		{"downscale with non-2:1 ratio rounds height", 300, 100, 150, 150, 50, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, tt.srcW, tt.srcH))
			got := resizeImageToWidth(src, tt.width)

			b := got.Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Fatalf("resized dims = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
		})
	}
}

func TestResizeToLongestSide(t *testing.T) {

	tests := []struct {
		name  string
		srcW  int
		srcH  int
		wantW int
		wantH int
	}{
		{"already smaller than blur target is untouched", 20, 10, 20, 10},
		{"exactly at threshold is untouched", BlurLongSide, 10, BlurLongSide, 10},
		{"wide image scales down by width", 320, 160, BlurLongSide, BlurLongSide / 2},
		{"tall image scales down by height", 160, 320, BlurLongSide / 2, BlurLongSide},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := image.NewRGBA(image.Rect(0, 0, tt.srcW, tt.srcH))
			got := resizeToLongestSide(src)

			b := got.Bounds()
			if b.Dx() != tt.wantW || b.Dy() != tt.wantH {
				t.Errorf("resized dims = %dx%d, want %dx%d", b.Dx(), b.Dy(), tt.wantW, tt.wantH)
			}
		})
	}
}

func TestClamp(t *testing.T) {

	tests := []struct {
		name        string
		v, min, max int
		want        int
	}{
		{"within range unchanged", 50, 1, 100, 50},
		{"below min clamps up", -5, 1, 100, 1},
		{"above max clamps down", 500, 1, 100, 100},
		{"equal to min", 1, 1, 100, 1},
		{"equal to max", 100, 1, 100, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clamp(tt.v, tt.min, tt.max); got != tt.want {
				t.Errorf("clamp(%d, %d, %d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
			}
		})
	}
}

func TestHasAlphaChannel(t *testing.T) {

	tests := []struct {
		name string
		img  image.Image
		want bool
	}{
		{"RGBA has alpha", image.NewRGBA(image.Rect(0, 0, 1, 1)), true},
		{"NRGBA has alpha", image.NewNRGBA(image.Rect(0, 0, 1, 1)), true},
		{"Alpha has alpha", image.NewAlpha(image.Rect(0, 0, 1, 1)), true},
		{"Gray has no alpha", image.NewGray(image.Rect(0, 0, 1, 1)), false},
		{"YCbCr has no alpha", image.NewYCbCr(image.Rect(0, 0, 1, 1), image.YCbCrSubsampleRatio420), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasAlphaChannel(tt.img); got != tt.want {
				t.Errorf("hasAlphaChannel(%T) = %v, want %v", tt.img, got, tt.want)
			}
		})
	}
}

func TestFlattenOnWhite(t *testing.T) {

	// fully transparent source pixel should become white after flattening
	src := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	src.Set(0, 0, color.NRGBA{R: 255, G: 0, B: 0, A: 0})   // transparent red -> should end up white
	src.Set(1, 0, color.NRGBA{R: 0, G: 255, B: 0, A: 255}) // opaque green -> should stay green

	got := flattenOnWhite(src)

	if r, g, b, a := got.At(0, 0).RGBA(); r != 0xffff || g != 0xffff || b != 0xffff || a != 0xffff {
		t.Errorf("transparent pixel = (%d,%d,%d,%d), want opaque white", r, g, b, a)
	}
	if r, g, b, _ := got.At(1, 0).RGBA(); r != 0 || g != 0xffff || b != 0 {
		t.Errorf("opaque green pixel = (%d,%d,%d), want (0,65535,0)", r, g, b)
	}
}

func TestEncodeToJpeg(t *testing.T) {

	t.Run("valid quality encodes and decodes back", func(t *testing.T) {
		src := image.NewRGBA(image.Rect(0, 0, 8, 8))
		out, err := encodeToJpeg(src, 90)
		if err != nil {
			t.Fatalf("encodeToJpeg() unexpected error: %v", err)
		}
		if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
			t.Errorf("encoded output does not decode as jpeg: %v", err)
		}
	})

	t.Run("out of range quality falls back to default", func(t *testing.T) {
		src := image.NewRGBA(image.Rect(0, 0, 8, 8))
		if _, err := encodeToJpeg(src, 0); err != nil {
			t.Fatalf("encodeToJpeg() with quality=0 unexpected error: %v", err)
		}
		if _, err := encodeToJpeg(src, 1000); err != nil {
			t.Fatalf("encodeToJpeg() with quality=1000 unexpected error: %v", err)
		}
	})

	t.Run("images with an alpha channel are flattened before encoding", func(t *testing.T) {
		src := image.NewNRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				src.Set(x, y, color.NRGBA{R: 10, G: 20, B: 30, A: 0})
			}
		}

		out, err := encodeToJpeg(src, JpegQuality)
		if err != nil {
			t.Fatalf("encodeToJpeg() unexpected error: %v", err)
		}

		decoded, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatalf("failed to decode jpeg output: %v", err)
		}
		// jpeg has no alpha channel by construction; just confirm it round-trips.
		if decoded.Bounds().Dx() != 4 || decoded.Bounds().Dy() != 4 {
			t.Errorf("decoded dims = %v, want 4x4", decoded.Bounds())
		}
	})
}

func TestNewImagePipeline(t *testing.T) {

	up := make(chan storage.WebhookPutObject)
	re := make(chan ReprocessCmd)
	de := make(chan DeletionCmd)
	var wg sync.WaitGroup

	repo := &mockRepository{}
	indexer := &mockIndexer{}
	cryptor := &mockCryptor{}
	objStore := &mockObjectStorage{}

	got := NewImagePipeline(up, re, de, &wg, repo, indexer, cryptor, objStore)
	if got == nil {
		t.Fatal("NewImagePipeline() returned nil")
	}

	impl, ok := got.(*imagePipeline)
	if !ok {
		t.Fatalf("expected concrete type *imagePipeline, got %T", got)
	}
	if impl.uploadQueue == nil || impl.reprocessQueue == nil || impl.deletionQueue == nil {
		t.Error("expected all three queues to be wired to the provided channels")
	}
	if impl.wg != &wg {
		t.Error("expected wg to be the exact pointer passed in")
	}
	if impl.db != repo {
		t.Error("expected the db field to be the exact Repository instance passed in")
	}
	if impl.indexer != indexer {
		t.Error("expected the indexer field to be the exact Indexer instance passed in")
	}
	if impl.cryptor != cryptor {
		t.Error("expected the cryptor field to be the exact Cryptor instance passed in")
	}
	if impl.objStore != objStore {
		t.Error("expected the objStore field to be the exact ObjectStorage instance passed in")
	}
	if impl.logger == nil {
		t.Error("expected the logger to be initialized")
	}
}
