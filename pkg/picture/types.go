package picture

import (
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
	"github.com/tdeslauriers/pixie/pkg/permission"
	"github.com/tdeslauriers/pixie/pkg/pipeline"
)

// Service is an aggregate interface that combines all picture-related services.
type Service interface {
	AlbumService
	AlbumImageService
	ImageService
	ImageServiceErr
	StagedImageService
}

func NewService(
	sql data.SqlRepository, 
	i data.Indexer, 
	c data.Cryptor, 
	obj storage.ObjectStorage, 
	q chan pipeline.ReprocessCmd) Service {
	return &service{
		AlbumService:       NewAlbumService(sql, i, c, obj),
		AlbumImageService:  NewAlbumImageService(sql, i, c),
		ImageService:       NewImageService(sql, i, c, obj, q),
		ImageServiceErr:    NewImageServiceErr(),
		StagedImageService: NewStagedImageService(sql, i, c, obj),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
type service struct {
	AlbumService
	AlbumImageService
	ImageService
	ImageServiceErr
	StagedImageService
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

// mapAlbumRecordsToApi is a helper function which maps album db records to album api structs
func mapAlbumRecordsToApi(albums []db.AlbumRecord) ([]api.Album, error) {
	if len(albums) == 0 {
		return nil, nil // no albums to map
	}

	apiAlbums := make([]api.Album, len(albums))
	for i, album := range albums {
		apiAlbum := api.Album{
			Id:          album.Id,
			Title:       album.Title,
			Description: album.Description,
			Slug:        album.Slug,
			CreatedAt:   album.CreatedAt,
			UpdatedAt:   album.UpdatedAt,
			IsArchived:  album.IsArchived,
		}
		apiAlbums[i] = apiAlbum
	}

	return apiAlbums, nil
}
