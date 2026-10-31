package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testKey = "channel-key"

type fakeAlliex struct {
	*httptest.Server
	uploads    atomic.Int32
	jobs       atomic.Int32
	lastJob    atomic.Value
	failUpload atomic.Bool
}

func newFakeAlliex(t *testing.T) *fakeAlliex {
	t.Helper()
	fake := &fakeAlliex{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testKey {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if fake.failUpload.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("upload form: %v", err)
			return
		}
		defer file.Close()
		fake.uploads.Add(1)
		fmt.Fprintf(w, `{"store_uri":"tos/%s","name":%q,"size":1,"account_index":2}`, header.Filename, header.Filename)
	})
	mux.HandleFunc("POST /api/video", func(w http.ResponseWriter, r *http.Request) {
		fake.jobs.Add(1)
		raw, _ := io.ReadAll(r.Body)
		fake.lastJob.Store(string(raw))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"jobs":[{"id":"job-1","status":"queued"}]}`)
	})
	mux.HandleFunc("GET /api/video/job-1", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"id":"job-1","status":"generating"}`)
	})
	mux.HandleFunc("GET /api/refs", func(w http.ResponseWriter, r *http.Request) {
		t.Error("route outside the allow list reached upstream")
	})
	fake.Server = httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	return fake
}

func startProxy(t *testing.T, fake *fakeAlliex, allowPrivate bool) *httptest.Server {
	t.Helper()
	upstream, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(newServer(config{
		upstream:            upstream,
		keyHash:             sha256.Sum256([]byte(testKey)),
		allowPrivateTargets: allowPrivate,
	}))
	t.Cleanup(proxy.Close)
	return proxy
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func imageHost(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}

func post(t *testing.T, target, key, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func job(references ...string) string {
	encoded, _ := json.Marshal(map[string]any{"prompt": "dance", "mode": "r2v", "count": 1, "reference_urls": references})
	return string(encoded)
}

func TestRejectsUnknownKey(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	for _, key := range []string{"", "other-key"} {
		if status, _ := post(t, proxy.URL+"/api/video", key, `{"prompt":"x"}`); status != http.StatusUnauthorized {
			t.Fatalf("key %q: status %d", key, status)
		}
	}
	if fake.jobs.Load() != 0 {
		t.Fatal("unauthorized job reached upstream")
	}
}

func TestPassesTextJobUnchanged(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	body := `{"prompt":"x","seed":12345678901234567890}`
	status, response := post(t, proxy.URL+"/api/video", testKey, body)
	if status != http.StatusAccepted || !strings.Contains(response, "job-1") {
		t.Fatalf("status %d: %s", status, response)
	}
	if got := fake.lastJob.Load(); got != body {
		t.Fatalf("job changed: %v", got)
	}
}

func TestUploadsReferencesInOrder(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	data := pngBytes(t)
	// The first image finishes last; order must follow the request anyway.
	host := imageHost(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.png" {
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write(data)
	})
	inline := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	status, response := post(t, proxy.URL+"/api/video", testKey, job(host.URL+"/slow.png", host.URL+"/fast.png", inline))
	if status != http.StatusAccepted {
		t.Fatalf("status %d: %s", status, response)
	}
	var sent struct {
		Prompt        string          `json:"prompt"`
		Images        []uploadedImage `json:"images"`
		ReferenceURLs []string        `json:"reference_urls"`
	}
	if err := json.Unmarshal([]byte(fake.lastJob.Load().(string)), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Prompt != "dance" || sent.ReferenceURLs != nil || len(sent.Images) != 3 {
		t.Fatalf("unexpected job: %+v", sent)
	}
	for index, uploaded := range sent.Images {
		name := fmt.Sprintf("reference-%d.png", index+1)
		if uploaded.Name != name || uploaded.StoreURI != "tos/"+name || uploaded.Account != 2 {
			t.Fatalf("image %d: %+v", index, uploaded)
		}
	}
}

func TestBadInputNeverCreatesJob(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	data := pngBytes(t)
	host := imageHost(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing.png":
			w.WriteHeader(http.StatusNotFound)
		case "/page.html":
			fmt.Fprint(w, "<html>not an image</html>")
		case "/huge.png":
			_, _ = w.Write(data)
			_, _ = w.Write(make([]byte, maxImageBytes))
		default:
			_, _ = w.Write(data)
		}
	})
	tooMany := make([]string, maxReferences+1)
	for index := range tooMany {
		tooMany[index] = host.URL + "/ok.png"
	}
	cases := map[string]string{
		"missing":    job(host.URL + "/missing.png"),
		"html":       job(host.URL + "/page.html"),
		"huge":       job(host.URL + "/huge.png"),
		"scheme":     job("file:///etc/passwd"),
		"credential": job("https://user:pass@example.com/a.png"),
		"data":       job("data:image/png,raw"),
		"empty":      job(),
		"too many":   job(tooMany...),
		"not array":  `{"reference_urls":"https://example.com/a.png"}`,
	}
	for name, body := range cases {
		if status, response := post(t, proxy.URL+"/api/video", testKey, body); status != http.StatusBadRequest {
			t.Errorf("%s: status %d: %s", name, status, response)
		}
	}
	if fake.jobs.Load() != 0 {
		t.Fatalf("%d jobs reached upstream", fake.jobs.Load())
	}
}

func TestUploadFailureHidesUpstream(t *testing.T) {
	fake := newFakeAlliex(t)
	fake.failUpload.Store(true)
	proxy := startProxy(t, fake, true)
	data := pngBytes(t)
	host := imageHost(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) })
	status, response := post(t, proxy.URL+"/api/video", testKey, job(host.URL+"/a.png"))
	if status != http.StatusBadGateway || fake.jobs.Load() != 0 {
		t.Fatalf("status %d jobs %d", status, fake.jobs.Load())
	}
	if strings.Contains(response, fake.URL) || !strings.Contains(response, "reference image upload failed") {
		t.Fatalf("leaked or unclear error: %s", response)
	}
}

func TestBlocksInternalTargets(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, false)
	internal := imageHost(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("internal address was fetched")
	})
	address, err := url.Parse(internal.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkTarget(address, false); err == nil {
		t.Fatal("non-standard port was allowed")
	}
	dialer := safeDialer(false)
	if _, err := dialer.Dial("tcp", address.Host); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("dial: %v", err)
	}
	for _, target := range []string{"http://127.0.0.1/a.png", "http://169.254.169.254/latest/meta-data", "http://[::1]/a.png"} {
		if status, _ := post(t, proxy.URL+"/api/video", testKey, job(target)); status != http.StatusBadRequest {
			t.Errorf("%s: status %d", target, status)
		}
	}
	for _, ip := range []string{"10.0.0.1", "172.16.0.1", "192.168.1.1", "100.64.0.1", "169.254.0.1", "127.0.0.1", "::1", "fc00::1", "0.0.0.0"} {
		if publicIP(net.ParseIP(ip)) {
			t.Errorf("%s treated as public", ip)
		}
	}
	if !publicIP(net.ParseIP("8.8.8.8")) {
		t.Error("public address rejected")
	}
}

func TestRedirectToBadSchemeIsRefused(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	host := imageHost(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "ftp://example.com/a.png", http.StatusFound)
	})
	if status, _ := post(t, proxy.URL+"/api/video", testKey, job(host.URL+"/a.png")); status != http.StatusBadRequest {
		t.Fatalf("status %d", status)
	}
}

func TestPassthroughRoutes(t *testing.T) {
	fake := newFakeAlliex(t)
	proxy := startProxy(t, fake, true)
	request := func(path, userAgent string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, proxy.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+testKey)
		req.Header.Set("User-Agent", userAgent)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(raw)
	}
	if status, body := request("/api/video/job-1", "curl/x"); status != http.StatusOK || !strings.Contains(body, "generating") {
		t.Fatalf("status %d: %s", status, body)
	}
	if status, _ := request("/api/refs", "curl/x"); status != http.StatusNotFound {
		t.Fatalf("status %d", status)
	}
}
