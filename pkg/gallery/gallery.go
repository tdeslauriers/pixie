package gallery

import (
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"sync"

	"log/slog"
	"net/http"
	"time"

	"github.com/tdeslauriers/carapace/pkg/config"
	"github.com/tdeslauriers/carapace/pkg/connect"
	"github.com/tdeslauriers/carapace/pkg/data"
	"github.com/tdeslauriers/carapace/pkg/diagnostics"
	"github.com/tdeslauriers/carapace/pkg/jwt"
	"github.com/tdeslauriers/carapace/pkg/pat"
	"github.com/tdeslauriers/carapace/pkg/session/provider"
	"github.com/tdeslauriers/carapace/pkg/sign"
	"github.com/tdeslauriers/carapace/pkg/storage"
	"github.com/tdeslauriers/pixie/internal/util"
	"github.com/tdeslauriers/pixie/pkg/notification"
	"github.com/tdeslauriers/pixie/pkg/patron"
	"github.com/tdeslauriers/pixie/pkg/permission"
	"github.com/tdeslauriers/pixie/pkg/picture"
	"github.com/tdeslauriers/pixie/pkg/pipeline"
)

// Gallery is the interface for engine that runs this service
type Gallery interface {

	// Run runs the gallery service
	Run() error

	// CloseDb closes the database connection
	CloseDb() error
}

// New creates a new Gallery service instance, returning a pointer to the concrete implementation.
func New(config *config.Config) (Gallery, error) {

	// server
	serverPki := &connect.Pki{
		CertFile: *config.Certs.ServerCert,
		KeyFile:  *config.Certs.ServerKey,
		CaFiles:  []string{*config.Certs.ServerCa},
	}

	serverTlsConfig, err := connect.NewTlsServerConfig(config.Tls, serverPki).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to configure %s task management service server tls: %v", config.ServiceName, err)
	}

	// client
	clientPki := &connect.Pki{
		CertFile: *config.Certs.ClientCert,
		KeyFile:  *config.Certs.ClientKey,
		CaFiles:  []string{*config.Certs.ClientCa},
	}

	// tls config for s2s client
	s2sClientConfig := connect.NewTlsClientConfig(clientPki)

	// s2s s2sClient
	s2sClient, err := connect.NewTlsClient(s2sClientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to configure s2s client config: %v", err)
	}

	// minio client
	objStorageConfig := storage.Config{
		Url:       config.ObjectStorage.Url,
		Bucket:    config.ObjectStorage.Bucket,
		AccessKey: config.ObjectStorage.AccessKey,
		SecretKey: config.ObjectStorage.SecretKey,
	}

	// tls config for minio client
	minioTlsConfig, err := connect.NewTlsClientConfig(clientPki).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to configure minio client tls: %v", err)
	}

	// object storage service
	// set default link expiration to 10 minutes
	objStore, err := storage.New(objStorageConfig, minioTlsConfig, 10*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("failed to create object storage service: %v", err)
	}

	// db client
	dbClientPki := &connect.Pki{
		CertFile: *config.Certs.DbClientCert,
		KeyFile:  *config.Certs.DbClientKey,
		CaFiles:  []string{*config.Certs.DbCaCert},
	}

	dbClientConfig, err := connect.NewTlsClientConfig(dbClientPki).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to configure database client tls: %v", err)
	}

	// db config
	dbUrl := data.DbUrl{
		Name:     config.Database.Name,
		Addr:     config.Database.Url,
		Username: config.Database.Username,
		Password: config.Database.Password,
	}

	db, err := data.NewSqlDbConnector(dbUrl, dbClientConfig).Connect()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	repository := data.NewSqlRepository(db)

	// indexer
	hmacSecret, err := base64.StdEncoding.DecodeString(config.Database.IndexSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hmac secret: %v", err)
	}

	indexer := data.NewIndexer(hmacSecret)

	// field level encryption
	aes, err := base64.StdEncoding.DecodeString(config.Database.FieldSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decode field level encryption secret: %v", err)
	}

	cryptor := data.NewServiceAesGcmKey(aes)

	// s2s jwt verifing key
	s2sPublicKey, err := sign.ParsePublicEcdsaCert(config.Jwt.S2sVerifyingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse s2s jwt verifying key: %v", err)
	}

	// jwt iamVerifier
	iamPublicKey, err := sign.ParsePublicEcdsaCert(config.Jwt.UserVerifyingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to parse iam verifying public key: %v", err)
	}

	// caller(s):
	// retry config for s2s callers
	retry := connect.RetryConfiguration{
		MaxRetries:  5,
		BaseBackoff: 100 * time.Microsecond,
		MaxBackoff:  10 * time.Second,
	}

	s2s := connect.NewS2sCaller(config.ServiceAuth.Url, util.ServiceS2s, s2sClient, retry)

	// s2s token provider
	s2sCreds := provider.S2sCredentials{
		ClientId:     config.ServiceAuth.ClientId,
		ClientSecret: config.ServiceAuth.ClientSecret,
	}

	tokenProvider := provider.NewS2sTokenProvider(s2s, s2sCreds, repository, cryptor)

	return &gallery{
		config:           *config,
		serverTls:        serverTlsConfig,
		repository:       repository,
		indexer:          indexer,
		cryptor:          cryptor,
		objectStorage:    objStore,
		s2sTokenProvider: tokenProvider,
		s2sVerifier:      jwt.NewVerifier(config.ServiceName, s2sPublicKey),
		iamVerifier:      jwt.NewVerifier(config.ServiceName, iamPublicKey),
		identity:         connect.NewS2sCaller(config.UserAuth.Url, util.ServiceIdentity, s2sClient, retry),
		patVerifier:      pat.NewVerifier(util.ServiceS2s, s2s, tokenProvider),
		pictures:         picture.NewService(repository, indexer, cryptor, objStore),
		patrons:          patron.NewService(repository, indexer, cryptor),
		permissions:      permission.NewService(repository, indexer, cryptor),

		queue: make(chan storage.WebhookPutObject, 100),

		logger: slog.Default().
			With(slog.String(util.ServiceKey, util.ServiceGallery)).
			With(slog.String(util.PackageKey, util.PackageGallery)).
			With(slog.String(util.ComponentKey, util.ComponentGallery)),
	}, nil
}

var _ Gallery = (*gallery)(nil)

// gallery is the concrete implementation of the Gallery interface.
type gallery struct {
	config           config.Config
	serverTls        *tls.Config
	repository       data.SqlRepository
	indexer          data.Indexer
	cryptor          data.Cryptor
	objectStorage    storage.ObjectStorage
	s2sTokenProvider provider.S2sTokenProvider
	s2sVerifier      jwt.Verifier
	iamVerifier      jwt.Verifier
	patVerifier      pat.Verifier
	identity         connect.S2sCaller
	pictures         picture.Service
	patrons          patron.Service
	permissions      permission.Service

	queue chan storage.WebhookPutObject
	wg    sync.WaitGroup

	logger *slog.Logger
}

// CloseDb closes the database connection.
func (g *gallery) CloseDb() error {
	if err := g.repository.Close(); err != nil {
		g.logger.Error(fmt.Sprintf("failed to close %s gallery database connection: %v", util.ServiceGallery, err))
	}
	return nil
}

// Run runs the gallery service.
func (g *gallery) Run() error {

	// image processing pipeline queue
	imgPipeline := pipeline.NewImagePipeline(g.queue, &g.wg, g.repository, g.indexer, g.cryptor, g.objectStorage)

	g.wg.Add(1)
	go imgPipeline.ProcessQueue()

	// register handlers
	mux := http.NewServeMux()
	mux.HandleFunc("/health", diagnostics.HealthCheckHandler)

	// album handlers
	pics := picture.NewHandler(g.pictures, g.permissions, g.s2sVerifier, g.iamVerifier)
	mux.HandleFunc("/albums", pics.HandleAlbums) // handles album listing and creation
	mux.HandleFunc("/albums/", pics.HandleAlbum) // trailing slash is so slugs can be appended to the path

	// image handlers
	mux.HandleFunc("/images/", pics.HandleImage) // trailing slash is so slugs can be appended to the path

	// notification handler
	notify := notification.NewHandler(g.queue, g.s2sVerifier, g.patVerifier)
	mux.HandleFunc("/images/notify/upload", notify.HandleImageUploadNotification)

	// patron handler
	pat := patron.NewHandler(g.patrons, g.s2sVerifier, g.iamVerifier)
	mux.HandleFunc("/patrons/permissions", pat.HandlePermissions) // retrieves users permissions and handles updates to patron permissions

	// s2s patron registration -> happens at time of registration, there will be no iam token
	mux.HandleFunc("/s2s/patrons/register", pat.HandleRegister) // handles patron registration -> ghost profile creation

	// permissions handler
	perm := permission.NewHandler(g.permissions, g.s2sVerifier, g.iamVerifier)
	mux.HandleFunc("/permissions", perm.HandlePermissions)
	mux.HandleFunc("/permissions/", perm.HandlePermission) // trailing slash is so slugs can be appended to the path

	galleryServer := &connect.TlsServer{
		Addr:      g.config.ServicePort,
		Mux:       mux,
		TlsConfig: g.serverTls,
	}

	go func() {

		g.logger.Info(fmt.Sprintf("starting %s gallery service on port %s", g.config.ServiceName, galleryServer.Addr[1:]))
		if err := galleryServer.Initialize(); err != http.ErrServerClosed {
			g.logger.Error(fmt.Sprintf("failed to start %s gallery service: %v", g.config.ServiceName, err))
		}
	}()

	// wait
	g.wg.Wait()
	close(g.queue)

	return nil
}
