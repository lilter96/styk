package ozon

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrFBSLabelDownload = errors.New("ozon FBS label download rejected")

const maxFBSLabelBytes = 8 << 20

// FBSLabelDownloader can fetch a previously reconciled PDF on the exact Seller
// API origin. No CDN origin is authorized by the current unverified schema.
// Construction does not authorize label create/get API requests.
type FBSLabelDownloader struct {
	http *http.Client
}

func NewFBSLabelDownloader(client *http.Client) (*FBSLabelDownloader, error) {
	if client == nil {
		return nil, ErrFBSLabelDownload
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if copyClient.Timeout <= 0 || copyClient.Timeout > 20*time.Second {
		copyClient.Timeout = 20 * time.Second
	}
	return &FBSLabelDownloader{http: &copyClient}, nil
}

// ValidateFBSLabelURL rejects arbitrary fetches, userinfo, port changes and
// redirects to other origins. It never resolves a user-supplied hostname.
func ValidateFBSLabelURL(raw string) error {
	if len(raw) == 0 || len(raw) > 8192 || strings.ContainsAny(raw, "\r\n\x00") {
		return ErrFBSLabelDownload
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || !strings.EqualFold(parsed.Host, "api-seller.ozon.ru") {
		return ErrFBSLabelDownload
	}
	if parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" || parsed.Path == "" || !strings.HasPrefix(parsed.Path, "/") {
		return ErrFBSLabelDownload
	}
	return nil
}

func (downloader *FBSLabelDownloader) Download(ctx context.Context, raw string) (data []byte, resultErr error) {
	if downloader == nil || downloader.http == nil || ValidateFBSLabelURL(raw) != nil {
		return nil, ErrFBSLabelDownload
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, ErrFBSLabelDownload
	}
	request.Header.Set("Accept", "application/pdf")
	response, err := downloader.http.Do(request)
	if err != nil {
		return nil, ErrFBSLabelDownload
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil && resultErr == nil {
			data, resultErr = nil, ErrFBSLabelDownload
		}
	}()
	return readFBSLabel(response)
}

func readFBSLabel(response *http.Response) ([]byte, error) {
	if response.StatusCode != http.StatusOK || response.ContentLength > maxFBSLabelBytes {
		return nil, ErrFBSLabelDownload
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/pdf" {
		return nil, ErrFBSLabelDownload
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxFBSLabelBytes+1))
	if err != nil || len(data) > maxFBSLabelBytes || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, ErrFBSLabelDownload
	}
	if response.ContentLength >= 0 && int64(len(data)) != response.ContentLength {
		return nil, ErrFBSLabelDownload
	}
	if !bytes.HasSuffix(bytes.TrimSpace(data), []byte("%%EOF")) {
		return nil, ErrFBSLabelDownload
	}
	return data, nil
}
