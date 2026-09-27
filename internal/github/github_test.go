package github

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/p0dxD/rendimiento.ai/internal/detect"
)

func newApp(t *testing.T, handler http.Handler) (*App, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	a, err := New(Credentials{AppID: 123, PrivateKey: string(pemKey)})
	if err != nil {
		t.Fatal(err)
	}
	if handler != nil {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		a.BaseURL = srv.URL
	}
	return a, key
}

func TestJWTIsValidRS256(t *testing.T) {
	a, key := newApp(t, nil)
	tok, err := a.JWT(time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt = %s", tok)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature invalid: %v", err)
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	json.Unmarshal(payload, &claims)
	if claims["iss"] != "123" || claims["iat"].(float64) != 1_700_000_000-60 {
		t.Fatalf("claims = %v", claims)
	}
}

func TestInstallationTokenCached(t *testing.T) {
	var calls atomic.Int32
	a, _ := newApp(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/7/access_tokens" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			http.Error(w, "bad", 400)
			return
		}
		calls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour)})
	}))
	for i := 0; i < 3; i++ {
		tok, err := a.InstallationToken(context.Background(), 7)
		if err != nil || tok != "ghs_x" {
			t.Fatalf("token = %q %v", tok, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("token fetched %d times", calls.Load())
	}
}

// fakeRepo serves a tree and blobs for a small monorepo, and records PR calls.
func fakeRepo(t *testing.T, files map[string]string, prCalls *[]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enc := json.NewEncoder(w)
		switch {
		case strings.HasSuffix(r.URL.Path, "/access_tokens"):
			enc.Encode(map[string]any{"token": "ghs_x", "expires_at": time.Now().Add(time.Hour)})
		case strings.Contains(r.URL.Path, "/git/trees/") && r.Method == http.MethodGet:
			var tree []map[string]any
			dirs := map[string]bool{}
			for p, c := range files {
				tree = append(tree, map[string]any{"path": p, "type": "blob", "sha": "blob:" + p, "size": len(c)})
				if i := strings.LastIndex(p, "/"); i > 0 && !dirs[p[:i]] {
					dirs[p[:i]] = true
					tree = append(tree, map[string]any{"path": p[:i], "type": "tree", "sha": "t"})
				}
			}
			enc.Encode(map[string]any{"tree": tree})
		case strings.Contains(r.URL.Path, "/git/blobs/"):
			p := strings.SplitN(r.URL.Path, "/git/blobs/blob:", 2)[1]
			enc.Encode(map[string]string{"content": base64.StdEncoding.EncodeToString([]byte(files[p])), "encoding": "base64"})
		case r.URL.Path == "/repos/o/r/git/ref/heads/main":
			enc.Encode(map[string]any{"object": map[string]string{"sha": "base-sha"}})
		case r.URL.Path == "/repos/o/r/git/commits/base-sha":
			enc.Encode(map[string]any{"tree": map[string]string{"sha": "base-tree"}})
		case r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			*prCalls = append(*prCalls, r.URL.Path+" "+string(b))
			switch r.URL.Path {
			case "/repos/o/r/git/refs":
				http.Error(w, `{"message":"Reference already exists"}`, http.StatusUnprocessableEntity)
			case "/repos/o/r/pulls":
				enc.Encode(map[string]any{"number": 5, "html_url": "https://github.com/o/r/pull/5"})
			default:
				enc.Encode(map[string]string{"sha": "new-sha"})
			}
		case r.Method == http.MethodPatch:
			*prCalls = append(*prCalls, "PATCH "+r.URL.Path)
			enc.Encode(map[string]string{})
		default:
			http.NotFound(w, r)
		}
	})
}

func TestRepoFSDrivesDetection(t *testing.T) {
	files := map[string]string{
		"README.md":            "hi",
		"api/requirements.txt": "fastapi\n",
		"api/app/main.py":      "",
		"web/package.json":     `{"scripts":{"build":"next build"},"dependencies":{"next":"15"}}`,
	}
	var calls []string
	a, _ := newApp(t, fakeRepo(t, files, &calls))
	rfs, err := a.Client(7).FS(context.Background(), "o/r", "main")
	if err != nil {
		t.Fatal(err)
	}
	rs, err := detect.Detect(rfs)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 2 || rs[0].Framework != "fastapi" || rs[0].Entrypoint != "app.main:app" || rs[1].Framework != "next" {
		t.Fatalf("detect over RepoFS = %+v", rs)
	}
}

func TestOpenPRSingleCommit(t *testing.T) {
	var calls []string
	a, _ := newApp(t, fakeRepo(t, nil, &calls))
	pr, err := a.Client(7).OpenPR(context.Background(), "o/r", "main", "rendimiento/onboard", "Deploy with rendimiento", "body",
		map[string]string{"rendimiento.yaml": "services: []", "Dockerfile": "FROM x"})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 5 {
		t.Fatalf("pr = %+v", pr)
	}
	joined := strings.Join(calls, "\n")
	for _, want := range []string{`"base_tree":"base-tree"`, `"parents":["base-sha"]`, "PATCH /repos/o/r/git/refs/heads/rendimiento/onboard", `"head":"rendimiento/onboard"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %s in calls:\n%s", want, joined)
		}
	}
}

func TestVerifyWebhook(t *testing.T) {
	body := []byte(`{"ref":"refs/heads/main"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !VerifyWebhook("s3cret", body, sig) {
		t.Fatal("valid signature rejected")
	}
	for _, bad := range []string{"", "sha256=00", sig[:len(sig)-2] + "00", strings.TrimPrefix(sig, "sha256=")} {
		if VerifyWebhook("s3cret", body, bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if VerifyWebhook("", body, sig) {
		t.Fatal("empty secret must reject")
	}
}
