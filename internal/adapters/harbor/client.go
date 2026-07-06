// Package harbor implements ports.ArtifactRegistry against a real Harbor
// container registry via the official goharbor/go-client SDK.
package harbor

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/url"
	"time"

	rcl "github.com/go-openapi/runtime/client"
	hcl "github.com/goharbor/go-client/pkg/sdk/v2.0/client"
	"github.com/goharbor/go-client/pkg/sdk/v2.0/client/ping"
	log "github.com/sirupsen/logrus"
)

// Config carries every knob the Harbor adapter needs, independent of how the
// application-wide config is structured.
type Config struct {
	URL                       *url.URL
	PageSize                  int64
	NumOfWorkersAllProjects   int
	NumOfWorkersAllRepos      int
	NumOfWorkersProjectRepos  int
	NumOfWorkersRepoArtifacts int
	Timeout                   time.Duration
	// InsecureSkipVerify disables TLS certificate verification - useful for a
	// local demo Harbor instance with a self-signed certificate. Never enable
	// this against a production registry.
	InsecureSkipVerify bool
}

// NewClient builds an authenticated Harbor SDK client and verifies connectivity.
func NewClient(cfg Config, login, password string) (*hcl.HarborAPI, error) {
	harborConfig := hcl.Config{
		URL:      cfg.URL,
		AuthInfo: rcl.BasicAuth(login, password),
	}
	if cfg.InsecureSkipVerify {
		harborConfig.Transport = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
	}

	client := hcl.New(harborConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := checkConnectivity(ctx, client); err != nil {
		return nil, err
	}
	return client, nil
}

func checkConnectivity(ctx context.Context, client *hcl.HarborAPI) error {
	log.Info("Checking connection to Harbor...")
	_, err := client.Ping.GetPing(ctx, &ping.GetPingParams{})
	if err != nil {
		log.Errorf("Connection to Harbor failed: %v", err)
		return err
	}
	log.Info("Connection to Harbor established, continuing...")
	return nil
}
