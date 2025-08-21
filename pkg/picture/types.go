package picture

import (
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/pkg/permission"
)

// Service is an aggregate interface that combines all picture-related services.
type Service interface {
	AlbumService
	ImageService
}

func NewService(sql data.SqlRepository, i data.Indexer, c data.Cryptor, obj storage.ObjectStorage) Service {
	return &service{
		AlbumService: NewAlbumService(sql, i, c, obj),
		ImageService: NewImageService(sql, i, c, obj),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
type service struct {
	AlbumService
	ImageService
}

// Handler is an aggregate interface that combines all picture-related handlers.
type Handler interface {
	AlbumHandler
	ImageHandler
}

// NewHandler creates a new Handler instance and returns a pointer to the concrete implementation.
func NewHandler(s Service, p permission.Service, s2s, iam jwt.Verifier) Handler {
	return &handler{
		AlbumHandler: NewAlbumHandler(s, p, s2s, iam),
		ImageHandler: NewImageHandler(s, p, s2s, iam),
	}
}

var _ Handler = (*handler)(nil)

// handler is the concrete implementation of the Handler interface.
type handler struct {
	AlbumHandler
	ImageHandler
}
