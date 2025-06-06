package image

// ImageData is a model that includes both the necessary fields from database record
// but also the signed url from the object storage service.
// It is used to return image data to the client in a single response.
type ImageData struct {
	// TODO: Add fields from the database record as needed

	SignedUrl string `json:"signed_url"` // The signed URL for the image, used to access the image in object storage
}
