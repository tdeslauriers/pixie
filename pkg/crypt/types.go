package crypt

import "github.com/tdeslauriers/carapace/pkg/data"

// Cryptor is an aggregate interface that combines the AlbumCryptor and ImageCryptor interfaces.
type Cryptor interface {
	AlbumCryptor
	AlbumImageCryptor
	ImageCryptor
}

func NewCryptor(c data.Cryptor) Cryptor {
	return &cryptor{
		AlbumCryptor:      NewAlbumCryptor(c),
		AlbumImageCryptor: NewAlbumImageCryptor(c),
		ImageCryptor:      NewImageCryptor(c),
	}
}

var _ Cryptor = (*cryptor)(nil)

// cryptor is the concrete implementation of the Cryptor interface, which
// embeds both the AlbumCryptor and ImageCryptor interfaces.
type cryptor struct {
	AlbumCryptor
	AlbumImageCryptor
	ImageCryptor
}
