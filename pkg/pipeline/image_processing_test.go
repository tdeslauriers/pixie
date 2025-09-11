package pipeline

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFilenameParse(t *testing.T) {

	input := "gallerydev/uploads/testtest-uuid-uuid-uuid-testuuidtest.jpg"

	dir := filepath.Dir(input)
	file := filepath.Base(input)
	ext := filepath.Ext(file)
	slug := strings.TrimSuffix(file, ext)

	if dir != "gallerydev/uploads" {
		t.Errorf("expected dir to be 'gallerydev/uploads', got '%s'", dir)
	}

	if file != "testtest-uuid-uuid-uuid-testuuidtest.jpg" {
		t.Errorf("expected file to be 'testtest-uuid-uuid-uuid-testuuidtest.jpg', got '%s'", file)
	}

	if ext != ".jpg" {
		t.Errorf("expected ext to be '.jpg', got '%s'", ext)
	}

	if slug != "testtest-uuid-uuid-uuid-testuuidtest" {
		t.Errorf("expected slug to be 'testtest-uuid-uuid-uuid-testuuidtest', got '%s'", slug)
	}
}
