package picture

import (
	"strings"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/permissions"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/pipeline"
	"github.com/tdeslauriers/pixie/internal/util"
)

// Service is an aggregate interface that combines all picture-related services.
type Service interface {
	AlbumImageService
	ImageService
	ImageServiceErr
}

func NewService(
	sql data.SqlRepository,
	i data.Indexer,
	c data.Cryptor,
	obj storage.ObjectStorage,
	q chan pipeline.ReprocessCmd) Service {
	return &service{

		AlbumImageService: NewAlbumImageService(sql, i, c),
		ImageService:      NewImageService(sql, i, c, obj, q),
		ImageServiceErr:   NewImageServiceErr(),
	}
}

var _ Service = (*service)(nil)

// service is the concrete implementation of the Service interface.
type service struct {
	AlbumImageService
	ImageService
	ImageServiceErr
}

type ImagePermissionXref struct {
	Id           int             `db:"id" json:"id"`                         // Unique identifier for the xref record
	ImageId      string          `db:"image_uuid" json:"image_id"`           // UUID of the image
	PermissionId string          `db:"permission_uuid" json:"permission_id"` // UUID of the permission
	CreatedAt    data.CustomTime `db:"created_at" json:"created_at"`         // Timestamp when the xref was created
}

// BuildGetImageQuery builds the SQL query to get an image by its slug and the user's permissions.
// It returns the query string and any parameters to be used in the query.
func BuildGetImageQuery(userPs map[string]permissions.PermissionRecord) string {

	var qb strings.Builder

	baseQry := `
		SELECT 
			i.uuid,
			i.title,
			i.description,
			i.file_name,
			i.file_type,
			i.object_key,
			i.slug,
			i.slug_index,
			i.width,
			i.height,
			i.size,
			i.image_date,
			i.created_at,
			i.updated_at,
			i.is_archived,
			i.is_published 
		FROM image i
			LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
			LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
		WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	// check if the user is a curator (admin)
	// if they are, then return the base query without any additional filters
	if _, ok := userPs[util.PermissionCurator]; ok {
		return qb.String()
	}

	// if the user is not a curator, need to add permission filters
	// makes a (?, ?, ?) list for the IN clause the length of the user's permissions
	if len(userPs) > 0 {
		qb.WriteString(" AND p.uuid IN (")
		for i := 0; i < len(userPs); i++ {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString(` ?`)
		}
		qb.WriteString(`)`)
	}

	// if the user is not a curator, they can only see published, non-archived images
	qb.WriteString(" AND i.is_published = TRUE")
	qb.WriteString(" AND i.is_archived = FALSE")

	return qb.String()
}

// BuildImageExistsQry builds an SQL query to check if an image exists based on its slug.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImageExistsQry() string {
	var qb strings.Builder

	// base query to check if the image exists
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
			WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	qb.WriteString(")")

	return qb.String()
}

// BuildImagePermissionsQry builds an sql query to check if an image exists but the user does not have permissions.
func BuildImagePermissionsQry(userPs map[string]permissions.PermissionRecord) string {
	var qb strings.Builder

	// base query to check if the image exists
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?`
	qb.WriteString(baseQry)

	// exclude images that have any of the user's permissions
	if len(userPs) > 0 {
		qb.WriteString(" AND (p.uuid IS NULL OR p.uuid NOT IN (")
		for i := 0; i < len(userPs); i++ {
			if i > 0 {
				qb.WriteString(", ")
			}
			qb.WriteString(` ?`)
		}
		qb.WriteString("))")
	}

	qb.WriteString(")")

	return qb.String()
}

// BuildImageArchivedQry builds an SQL query to check if an image is archived.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImageArchivedQry() string {
	var qb strings.Builder

	// base query to check if the image is archived
	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?
				AND i.is_archived = TRUE`
	qb.WriteString(baseQry)

	return qb.String()
}

// BuildImagePublishedQry builds an SQL query to check if an image is published.
// It returns the query string and any parameters to be used in the query.
// Note: at the moment, no params are needed or constructed, but this may change in the future.
func BuildImagePublishedQry() string {
	var qb strings.Builder

	baseQry := `
		SELECT EXISTS (
			SELECT 1 
			FROM image i
				LEFT OUTER JOIN image_permission ip ON i.uuid = ip.image_uuid
				LEFT OUTER JOIN permission p ON ip.permission_uuid = p.uuid
			WHERE i.slug_index = ?
				AND i.is_published = FALSE`
	qb.WriteString(baseQry)

	return qb.String()
}
