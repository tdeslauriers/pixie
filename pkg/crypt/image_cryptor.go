package crypt

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/adaptors/db"
	"github.com/tdeslauriers/pixie/pkg/api"
)

// ImageCryptor is the interface for encrypting and decrypting image specific data fields.
type ImageCryptor interface {

	// EncryptImageRecord encrypts sensitive fields in the ImageRecord struct.
	EncryptImageRecord(image *db.ImageRecord) error

	// EncryptImageData encrypts sensitive fields in the ImageData struct.
	EncryptImageData(image *api.ImageData) error

	// DecryptImageData decrypts sensitive fields in the ImageData struct.
	DecryptImageData(image *api.ImageData) error

	// DecryptImageRecord decrypts sensitive fields in the ImageRecord struct.
	DecryptImageRecord(image *db.ImageRecord) error
}

// NewCryptor creates a new ImageCryptor instance, returning a pointer to the concrete implementation.
func NewImageCryptor(c data.Cryptor) ImageCryptor {
	return &imageCryptor{
		cryptor: c,

		logger: slog.Default().
			With(slog.String(util.PackageKey, util.PackageService)).
			With(slog.String(util.ComponentKey, util.ComponentImageCryptor)).
			With(slog.String(util.ServiceKey, util.ServiceGallery)),
	}
}

var _ ImageCryptor = (*imageCryptor)(nil)

// imageCryptor is the concrete implementation of the Cryptor interface, which
// is a wrapper around the data.Cryptor interface from carapace, providing
// image-specific encryption and decryption methods.
type imageCryptor struct {
	cryptor data.Cryptor

	logger *slog.Logger
}

// EncryptImageRecord encrypts sensitive fields in the ImageRecord struct.
func (ic *imageCryptor) EncryptImageRecord(image *db.ImageRecord) error {
	if image == nil {
		return fmt.Errorf("image record cannot be nil")
	}

	// encrypt the sensitive fields in the image record
	if err := ic.encryptImageFieldData(
		&image.Title,
		&image.Description,
		&image.FileName,
		&image.ImageDate,
		&image.ObjectKey,
		&image.Slug,
	); err != nil {
		return fmt.Errorf("failed to encrypt image record '%s': %v", image.Id, err)
	}

	return nil
}

// EncryptImageData encrypts sensitive fields in the ImageData struct.
func (ic *imageCryptor) EncryptImageData(image *api.ImageData) error {

	if image == nil {
		return fmt.Errorf("image data cannot be nil")
	}

	// encrypt the sensitive fields in the image data
	if err := ic.encryptImageFieldData(
		&image.Title,
		&image.Description,
		&image.FileName,
		&image.ImageDate,
		&image.ObjectKey,
		&image.Slug,
	); err != nil {
		return fmt.Errorf("failed to encrypt image data '%s': %v", image.Id, err)
	}

	return nil
}

// encryptImageData encrypts sensitive fields in the in image metadata models.
func (ic *imageCryptor) encryptImageFieldData(
	titlePtr *string,
	descriptionPtr *string,
	fileNamePtr *string,
	imageDatePtr *string,
	objectKeyPtr *string,
	slugPtr *string,
) error {

	// encrypt the sensitive fields in the updated image record
	var (
		wg sync.WaitGroup

		titleCh   = make(chan string, 1)
		descCh    = make(chan string, 1)
		fnCh      = make(chan string, 1) // file name is encrypted because it is made of the slug
		imgDateCh = make(chan string, 1) // image date is encrypted to prevent leakage of sensitive information due to low numbers of images in certain years.
		objKeyCh  = make(chan string, 1) // object key may have changed if the image was unpublished or if the upload pipeline failed.
		slugCh    = make(chan string, 1)

		errCh = make(chan error, 6) // to capture any errors from encryption
	)

	wg.Add(5)
	go ic.encrypt(*titlePtr, "image title", titleCh, errCh, &wg)
	go ic.encrypt(*descriptionPtr, "image description", descCh, errCh, &wg)
	go ic.encrypt(*fileNamePtr, "file name", fnCh, errCh, &wg)
	go ic.encrypt(*objectKeyPtr, "image object key", objKeyCh, errCh, &wg)
	go ic.encrypt(*slugPtr, "image slug", slugCh, errCh, &wg)

	// there are places where the image date may not exist yet, so we check if it is empty
	if imageDatePtr != nil && *imageDatePtr != "" {
		wg.Add(1)
		go ic.encrypt(*imageDatePtr, "image date", imgDateCh, errCh, &wg)
	}

	// wait for all goroutines to finish
	wg.Wait()
	close(titleCh)
	close(descCh)
	close(fnCh)
	close(imgDateCh) // need to close this channel even if it was not used
	close(objKeyCh)  // need to close this channel even if it was not used
	close(slugCh)
	close(errCh)

	// check for any errors during encryption
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return fmt.Errorf("failed to encrypt image fields: %s", strings.Join(errs, "; "))
	}

	// assign the encrypted values to the pointers
	if titlePtr != nil {
		if title, ok := <-titleCh; ok {
			*titlePtr = title
		}
	}

	if descriptionPtr != nil {
		if desc, ok := <-descCh; ok {
			*descriptionPtr = desc
		}
	}

	if fileNamePtr != nil {
		if fn, ok := <-fnCh; ok {
			*fileNamePtr = fn
		}
	}

	if imageDatePtr != nil {
		if imgDate, ok := <-imgDateCh; ok {
			*imageDatePtr = imgDate
		}
	}

	if objectKeyPtr != nil {
		if objKey, ok := <-objKeyCh; ok {
			*objectKeyPtr = objKey
		}
	}

	if slugPtr != nil {
		if slug, ok := <-slugCh; ok {
			*slugPtr = slug
		}
	}

	return nil
}

// encrypt is a helper function that encrypts the sensitive fields for the image service.
// mostly it exists so the code looks cleaner and more readable.
func (ic *imageCryptor) encrypt(plaintext, fieldname string, encCh chan string, errCh chan error, wg *sync.WaitGroup) {
	defer wg.Done()

	if plaintext == "" {
		errCh <- fmt.Errorf("failed to encrypt '%s' field because it is empty", fieldname)
		return
	}

	// encrypt the plaintext
	ciphertext, err := ic.cryptor.EncryptServiceData([]byte(plaintext))
	if err != nil {
		ic.logger.Error(fmt.Sprintf("failed to encryp %s '%s': %v", fieldname, plaintext, err))
		errCh <- err
		return
	}

	// send the ciphertext to the channel
	encCh <- ciphertext
}

// DecryptImageData is a wrapper function that decrypts sensitive fields in the ImageData struct.
func (ic *imageCryptor) DecryptImageData(image *api.ImageData) error {
	if image == nil {
		return fmt.Errorf("image data cannot be nil")
	}

	// decrypt the sensitive fields in the image data
	if err := ic.decryptImageFields(
		&image.Title,
		&image.Description,
		&image.FileName,
		&image.ObjectKey,
		&image.Slug,
		&image.ImageDate,
	); err != nil {
		return fmt.Errorf("failed to decrypt image data '%s': %v", image.Id, err)
	}

	return nil
}

// DecryptImageRecord is a wrapper function that decrypts sensitive fields in the ImageRecord struct.
func (ic *imageCryptor) DecryptImageRecord(image *db.ImageRecord) error {
	if image == nil {
		return fmt.Errorf("image record cannot be nil")
	}

	// decrypt the sensitive fields in the image record
	if err := ic.decryptImageFields(
		&image.Title,
		&image.Description,
		&image.FileName,
		&image.ObjectKey,
		&image.Slug,
		&image.ImageDate,
	); err != nil {
		return fmt.Errorf("failed to decrypt image record '%s': %v", image.Id, err)
	}

	return nil
}

// decryptImageFields decrypts the sensitive fields of an image record.
func (ic *imageCryptor) decryptImageFields(
	titlePtr *string,
	descriptionPtr *string,
	fileNamePtr *string,
	objectKeyPtr *string,
	slugPtr *string,
	imageDatePtr *string,
) error {

	var (
		wg sync.WaitGroup

		titleCh = make(chan string, 1)
		descCh  = make(chan string, 1)
		fnCh    = make(chan string, 1) // file name is encrypted because it is made of the slug
		okCh    = make(chan string, 1) // object key is encrypted because it is made of the slug
		slugCh  = make(chan string, 1)
		IdCh    = make(chan string, 1) // image date is encrypted

		errCh = make(chan error, 6) // to capture any errors from decryption
	)

	wg.Add(5)
	go ic.decrypt(*titlePtr, "image title", titleCh, errCh, &wg)
	go ic.decrypt(*descriptionPtr, "image description", descCh, errCh, &wg)
	go ic.decrypt(*fileNamePtr, "image filename", fnCh, errCh, &wg)
	go ic.decrypt(*objectKeyPtr, "image object key", okCh, errCh, &wg)
	go ic.decrypt(*slugPtr, "image slug", slugCh, errCh, &wg)

	// may not exist yet or may have been an error in processing pipeline reading exif data
	if *imageDatePtr != "" {
		wg.Add(1)
		go ic.decrypt(*imageDatePtr, "image date", IdCh, errCh, &wg)
	}

	// wait for all goroutines to finish
	wg.Wait()
	close(titleCh)
	close(descCh)
	close(fnCh)
	close(okCh)
	close(slugCh)
	close(IdCh)
	close(errCh)

	// check for any errors during decryption
	if len(errCh) > 0 {
		errs := make([]string, 0, len(errCh)+1)
		for err := range errCh {
			errs = append(errs, err.Error())
		}
		return fmt.Errorf("failed to decrypt image data field value(s): %s", strings.Join(errs, "; "))
	}

	// assign the decrypted values to the pointers
	if titlePtr != nil {
		if title, ok := <-titleCh; ok {
			*titlePtr = title
		}
	}

	if descriptionPtr != nil {
		if desc, ok := <-descCh; ok {
			*descriptionPtr = desc
		}
	}

	if fileNamePtr != nil {
		if fn, ok := <-fnCh; ok {
			*fileNamePtr = fn
		}
	}

	if objectKeyPtr != nil {
		if objKey, ok := <-okCh; ok {
			*objectKeyPtr = objKey
		}
	}

	if slugPtr != nil {
		if slug, ok := <-slugCh; ok {
			*slugPtr = slug
		}
	}

	if imageDatePtr != nil {
		if imageDate, ok := <-IdCh; ok {
			*imageDatePtr = imageDate
		}
	}

	return nil
}

// decrypt is a helper function that decrypts the sensitive fields for the image service.
func (ic *imageCryptor) decrypt(ciphertext, fieldname string, decCh chan string, errCh chan error, wg *sync.WaitGroup) {

	defer wg.Done()

	if ciphertext == "" {
		errCh <- fmt.Errorf("failed to decrypt '%s' field because it is empty", fieldname)
		return
	}

	// decrypt the ciphertext
	plaintext, err := ic.cryptor.DecryptServiceData(ciphertext)
	if err != nil {
		ic.logger.Error(fmt.Sprintf("failed to decrypt %s: %v", fieldname, err))
		errCh <- err
		return
	}

	// send the plaintext to the channel
	decCh <- string(plaintext)
}
