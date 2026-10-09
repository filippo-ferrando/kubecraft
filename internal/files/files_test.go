package files

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func setup(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	outside := t.TempDir()
	mk := func(p, content string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("server.properties", "motd=hi\n")
	mk("plugins/Chunky/config.yml", "language: en\n")
	mk("plugins/Chunky.jar", "binary")
	mk("world/level.dat", "level")
	mk("world/session.lock", "lock")
	mk("world/region/r.0.0.mca", "region")
	mk("world_nether/level.dat", "nether")
	mk("libraries/big.jar", "lib")
	mk("paper.jar", "server jar")
	if err := os.WriteFile(filepath.Join(outside, "secret.yml"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.yml"), filepath.Join(dir, "plugins", "link.yml")); err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(dir, "tok")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv, dir
}

func call(t *testing.T, srv *httptest.Server, method, p string, q url.Values, body string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+p+"?"+q.Encode(), strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, b
}

func TestAuth(t *testing.T) {
	srv, _ := setup(t)
	res, err := http.Get(srv.URL + "/list")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", res.StatusCode)
	}
	if _, err := NewHandler(t.TempDir(), ""); err == nil {
		t.Fatal("empty token accepted")
	}
}

func TestListReadWrite(t *testing.T) {
	srv, dir := setup(t)
	res, b := call(t, srv, "GET", "/list", url.Values{"path": {"plugins"}}, "")
	if res.StatusCode != 200 {
		t.Fatalf("list: %d %s", res.StatusCode, b)
	}
	var l struct {
		Entries []Entry `json:"entries"`
	}
	_ = json.Unmarshal(b, &l)
	got := map[string]Entry{}
	for _, e := range l.Entries {
		got[e.Name] = e
	}
	if !got["Chunky"].Dir || got["Chunky.jar"].Editable || got["link.yml"].Editable {
		t.Fatalf("entries: %+v", l.Entries)
	}

	q := url.Values{"path": {"plugins/Chunky/config.yml"}}
	res, b = call(t, srv, "GET", "/read", q, "")
	if res.StatusCode != 200 || !strings.Contains(string(b), "language: en") {
		t.Fatalf("read: %d %s", res.StatusCode, b)
	}
	if res, _ := call(t, srv, "PUT", "/write", q, "language: it\n"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("write: %d", res.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins/Chunky/config.yml")); string(b) != "language: it\n" {
		t.Fatalf("content: %q", b)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "plugins/Chunky")); len(entries) != 1 {
		t.Fatal("temp file left behind")
	}
}

func TestRejects(t *testing.T) {
	srv, _ := setup(t)
	cases := []struct {
		method, path, file string
	}{
		{"GET", "/read", "../../etc/passwd.txt"},
		{"GET", "/read", "plugins/link.yml"}, // symlink out of the root
		{"PUT", "/write", "plugins/link.yml"},
		{"GET", "/read", "plugins/Chunky.jar"}, // not editable
		{"PUT", "/write", "plugins/new.yml"},   // only existing files
		{"PUT", "/write", "paper.jar"},
	}
	for _, c := range cases {
		res, b := call(t, srv, c.method, c.path, url.Values{"path": {c.file}}, "x")
		if res.StatusCode < 400 {
			t.Errorf("%s %s %s accepted: %s", c.method, c.path, c.file, b)
		}
		if strings.Contains(string(b), "secret") {
			t.Errorf("%s leaked outside content", c.file)
		}
	}
	// ../ is clamped to the root, never outside it.
	res, b := call(t, srv, "GET", "/list", url.Values{"path": {"../../.."}}, "")
	if res.StatusCode != 200 || !strings.Contains(string(b), "server.properties") {
		t.Fatalf("clamped list: %d %s", res.StatusCode, b)
	}
	big := strings.Repeat("a", MaxEditSize+10)
	if res, _ := call(t, srv, "PUT", "/write", url.Values{"path": {"server.properties"}}, big); res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("big write: %d", res.StatusCode)
	}
	if res, _ := call(t, srv, "PUT", "/write", url.Values{"path": {"server.properties"}}, "\xff\xfe"); res.StatusCode != http.StatusBadRequest {
		t.Fatalf("binary write: %d", res.StatusCode)
	}
}

func tarNames(t *testing.T, b []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
}

func TestBackup(t *testing.T) {
	srv, _ := setup(t)
	res, b := call(t, srv, "GET", "/backup", url.Values{"scope": {"world"}}, "")
	if res.StatusCode != 200 {
		t.Fatalf("backup: %d", res.StatusCode)
	}
	names := tarNames(t, b)
	for _, want := range []string{"world/level.dat", "world/region/r.0.0.mca", "world_nether/level.dat", "server.properties"} {
		if !slices.Contains(names, want) {
			t.Errorf("world backup missing %s: %v", want, names)
		}
	}
	for _, bad := range []string{"world/session.lock", "plugins/Chunky.jar", "paper.jar"} {
		if slices.Contains(names, bad) {
			t.Errorf("world backup has %s", bad)
		}
	}

	_, b = call(t, srv, "GET", "/backup", url.Values{"scope": {"full"}}, "")
	names = tarNames(t, b)
	if !slices.Contains(names, "plugins/Chunky/config.yml") || !slices.Contains(names, "plugins/Chunky.jar") {
		t.Errorf("full backup incomplete: %v", names)
	}
	for _, bad := range []string{"libraries/big.jar", "paper.jar", "plugins/link.yml"} {
		if slices.Contains(names, bad) {
			t.Errorf("full backup has %s", bad)
		}
	}
	if res, _ := call(t, srv, "GET", "/backup", url.Values{"scope": {"etc"}}, ""); res.StatusCode != http.StatusBadRequest {
		t.Fatal("bad scope accepted")
	}
}

func TestBackupNoWorld(t *testing.T) {
	h, err := NewHandler(t.TempDir(), "tok")
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	srv := httptest.NewServer(h)
	defer srv.Close()
	if res, _ := call(t, srv, "GET", "/backup", nil, ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", res.StatusCode)
	}
}
