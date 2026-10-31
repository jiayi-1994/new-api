// Alliex only accepts reference images uploaded through /api/upload, and a New
// API task plugin can send just one request per submission. This service sits
// between them: it turns reference_urls into uploaded images, then forwards
// the job. Every other allowed route passes through untouched.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	maxReferences   = 30
	maxImageBytes   = 30 << 20 // documented limit for one upload
	maxTotalBytes   = 128 << 20
	maxBodyBytes    = 16 << 20 // inline data: URLs arrive in the job body
	uploadWorkers   = 4
	imageTimeout    = 60 * time.Second
	prepareTimeout  = 180 * time.Second
	forwardTimeout  = 60 * time.Second
	maxRedirects    = 3
	maxResponseBody = 1 << 20
)

// Only what the plugin calls. Anything else stays unreachable through here.
var passthroughRoutes = []*regexp.Regexp{
	regexp.MustCompile(`^GET /api/video/[^/]+$`),
	regexp.MustCompile(`^POST /api/video/[^/]+/query$`),
}

type config struct {
	upstream *url.URL
	keyHash  [sha256.Size]byte
	// Tests serve images from loopback; production never sets this.
	allowPrivateTargets bool
}

type server struct {
	config   config
	upstream *http.Client
	images   *http.Client
	proxy    *httputil.ReverseProxy
}

func newServer(cfg config) *server {
	images := &http.Client{
		Timeout: imageTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           safeDialer(cfg.allowPrivateTargets).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return errors.New("too many redirects")
			}
			return checkTarget(req.URL, cfg.allowPrivateTargets)
		},
	}
	proxy := httputil.NewSingleHostReverseProxy(cfg.upstream)
	baseDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		baseDirector(req)
		req.Host = cfg.upstream.Host
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		log.Printf("forward failed path=%s: %v", r.URL.Path, err)
		writeError(w, http.StatusBadGateway, "upstream request failed")
	}
	return &server{config: cfg, upstream: &http.Client{Timeout: prepareTimeout}, images: images, proxy: proxy}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodPost && r.URL.Path == "/api/video" {
		s.submit(w, r)
		return
	}
	route := r.Method + " " + r.URL.Path
	for _, allowed := range passthroughRoutes {
		if allowed.MatchString(route) {
			s.proxy.ServeHTTP(w, r)
			return
		}
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *server) authorized(r *http.Request) bool {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || token == "" {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(hash[:], s.config.keyHash[:]) == 1
}

func (s *server) submit(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		return
	}
	body := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	if encoded, present := body["reference_urls"]; present {
		var references []string
		if err := json.Unmarshal(encoded, &references); err != nil {
			writeError(w, http.StatusBadRequest, "reference_urls must be a string array")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), prepareTimeout)
		defer cancel()
		images, err := s.prepareImages(ctx, r.Header.Get("Authorization"), references)
		if err != nil {
			var input inputError
			if errors.As(err, &input) {
				writeError(w, http.StatusBadRequest, input.Error())
				return
			}
			log.Printf("prepare references failed count=%d: %v", len(references), err)
			writeError(w, http.StatusBadGateway, "reference image upload failed")
			return
		}
		delete(body, "reference_urls")
		if body["images"], err = json.Marshal(images); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode request")
			return
		}
		if raw, err = json.Marshal(body); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to encode request")
			return
		}
	}
	// Never retried: a repeated job could be billed twice upstream.
	ctx, cancel := context.WithTimeout(r.Context(), forwardTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.upstreamURL("/api/video"), bytes.NewReader(raw))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to build request")
		return
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.upstream.Do(req)
	if err != nil {
		log.Printf("submit failed: %v", err)
		writeError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (s *server) upstreamURL(path string) string {
	return strings.TrimRight(s.config.upstream.String(), "/") + path
}

type uploadedImage struct {
	StoreURI string `json:"store_uri"`
	Name     string `json:"name"`
	Account  int64  `json:"account"`
}

// inputError is a problem with what the caller sent, safe to show to them.
type inputError struct{ message string }

func (e inputError) Error() string { return e.message }

type sharedBudget struct {
	mu        sync.Mutex
	remaining int64
}

func (b *sharedBudget) spend(size int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if size > b.remaining {
		return false
	}
	b.remaining -= size
	return true
}

// prepareImages keeps the input order, because prompts address images by
// position ("@参考图1").
func (s *server) prepareImages(ctx context.Context, authorization string, references []string) ([]uploadedImage, error) {
	if len(references) == 0 || len(references) > maxReferences {
		return nil, inputError{fmt.Sprintf("reference_urls must contain 1 to %d images", maxReferences)}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]uploadedImage, len(references))
	budget := &sharedBudget{remaining: maxTotalBytes}
	slots := make(chan struct{}, uploadWorkers)
	var wait sync.WaitGroup
	var once sync.Once
	var failure error
	for index, reference := range references {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				return
			}
			image, err := s.prepareImage(ctx, authorization, index, reference, budget)
			if err != nil {
				once.Do(func() { failure = err; cancel() })
				return
			}
			results[index] = image
		}()
	}
	wait.Wait()
	if failure != nil {
		return nil, failure
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func (s *server) prepareImage(ctx context.Context, authorization string, index int, reference string, budget *sharedBudget) (uploadedImage, error) {
	data, err := s.readImage(ctx, reference, budget)
	if err != nil {
		return uploadedImage{}, err
	}
	extension, mimeType := imageType(data)
	if extension == "" {
		return uploadedImage{}, inputError{fmt.Sprintf("reference image %d is not a PNG, JPEG or WebP image", index+1)}
	}
	name := fmt.Sprintf("reference-%d%s", index+1, extension)
	return s.upload(ctx, authorization, name, mimeType, data)
}

func (s *server) readImage(ctx context.Context, reference string, budget *sharedBudget) ([]byte, error) {
	if strings.HasPrefix(reference, "data:") {
		return decodeDataURL(reference, budget)
	}
	target, err := url.Parse(reference)
	if err != nil {
		return nil, inputError{"reference image URL is invalid"}
	}
	if err := checkTarget(target, s.config.allowPrivateTargets); err != nil {
		return nil, inputError{"reference image URL is not allowed"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, inputError{"reference image URL is invalid"}
	}
	resp, err := s.images.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		log.Printf("image download failed host=%s: %v", target.Hostname(), err)
		return nil, inputError{"reference image could not be downloaded"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, inputError{"reference image could not be downloaded"}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, inputError{"reference image could not be downloaded"}
	}
	if len(data) > maxImageBytes {
		return nil, inputError{"reference image exceeds 30 MB"}
	}
	if !budget.spend(int64(len(data))) {
		return nil, inputError{"reference images are too large in total"}
	}
	return data, nil
}

func decodeDataURL(reference string, budget *sharedBudget) ([]byte, error) {
	header, payload, found := strings.Cut(reference, ",")
	if !found || !strings.HasSuffix(strings.ToLower(header), ";base64") {
		return nil, inputError{"reference image data URL must be base64"}
	}
	if int64(base64.StdEncoding.DecodedLen(len(payload))) > maxImageBytes+2 {
		return nil, inputError{"reference image exceeds 30 MB"}
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, inputError{"reference image data URL is invalid"}
	}
	if len(data) > maxImageBytes {
		return nil, inputError{"reference image exceeds 30 MB"}
	}
	if !budget.spend(int64(len(data))) {
		return nil, inputError{"reference images are too large in total"}
	}
	return data, nil
}

func imageType(data []byte) (extension, mimeType string) {
	switch http.DetectContentType(data) {
	case "image/png":
		return ".png", "image/png"
	case "image/jpeg":
		return ".jpg", "image/jpeg"
	case "image/webp":
		return ".webp", "image/webp"
	}
	return "", ""
}

func (s *server) upload(ctx context.Context, authorization, name, mimeType string, data []byte) (uploadedImage, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	header.Set("Content-Type", mimeType)
	part, err := form.CreatePart(header)
	if err != nil {
		return uploadedImage{}, err
	}
	if _, err := part.Write(data); err != nil {
		return uploadedImage{}, err
	}
	if err := form.Close(); err != nil {
		return uploadedImage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.upstreamURL("/api/upload"), &body)
	if err != nil {
		return uploadedImage{}, err
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := s.upstream.Do(req)
	if err != nil {
		return uploadedImage{}, err
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return uploadedImage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return uploadedImage{}, fmt.Errorf("upload returned status %d", resp.StatusCode)
	}
	var uploaded struct {
		StoreURI     string `json:"store_uri"`
		AccountIndex *int64 `json:"account_index"`
	}
	if err := json.Unmarshal(payload, &uploaded); err != nil {
		return uploadedImage{}, fmt.Errorf("decode upload response: %w", err)
	}
	// The response has no ok field: store_uri is the success signal.
	if uploaded.StoreURI == "" || uploaded.AccountIndex == nil {
		return uploadedImage{}, errors.New("upload response has no store_uri or account_index")
	}
	return uploadedImage{StoreURI: uploaded.StoreURI, Name: name, Account: *uploaded.AccountIndex}, nil
}

func checkTarget(target *url.URL, anyPort bool) error {
	if target.Scheme != "https" && target.Scheme != "http" {
		return errors.New("unsupported scheme")
	}
	if target.Hostname() == "" || target.User != nil {
		return errors.New("invalid host")
	}
	if port := target.Port(); !anyPort && port != "" && port != "80" && port != "443" {
		return errors.New("unsupported port")
	}
	return nil
}

var carrierGradeNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

func publicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !carrierGradeNAT.Contains(ip)
}

// safeDialer checks the address actually connected to, so a hostname that
// resolves or rebinds to an internal address is refused, on redirects as well.
func safeDialer(allowPrivate bool) *net.Dialer {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if allowPrivate {
		return dialer
	}
	dialer.Control = func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		ip := net.ParseIP(host)
		if ip == nil || !publicIP(ip) {
			return errors.New("target address is not public")
		}
		return nil
	}
	return dialer
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func loadConfig() (config, error) {
	upstream, err := url.Parse(strings.TrimRight(os.Getenv("ALLIEX_UPSTREAM"), "/"))
	if err != nil || upstream.Scheme != "https" || upstream.Host == "" {
		return config{}, errors.New("ALLIEX_UPSTREAM must be an https URL")
	}
	// A hash, so this service never stores the channel key itself.
	decoded, err := hex.DecodeString(strings.TrimSpace(os.Getenv("ALLIEX_KEY_SHA256")))
	if err != nil || len(decoded) != sha256.Size {
		return config{}, errors.New("ALLIEX_KEY_SHA256 must be a SHA-256 hex digest")
	}
	cfg := config{upstream: upstream}
	copy(cfg.keyHash[:], decoded)
	return cfg, nil
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatal(err)
	}
	address := os.Getenv("LISTEN_ADDR")
	if address == "" {
		address = "127.0.0.1:8787"
	}
	httpServer := &http.Server{
		Addr:              address,
		Handler:           newServer(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("listening on %s", address)
	log.Fatal(httpServer.ListenAndServe())
}
