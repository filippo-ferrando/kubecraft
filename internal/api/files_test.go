package api

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/files"
	"git.fferrando.cc/rdfilippo/kubecraft/internal/k8s"
)

// fakeFiles serves a real file handler on a temp dir, standing in for the sidecar.
type fakeFiles struct{ addr string }

func (f fakeFiles) FilesAddr(context.Context, k8s.ServerSpec, bool) (string, error) {
	return f.addr, nil
}
func (f fakeFiles) FilesToken(context.Context, string) (string, error) { return "tok", nil }

func withFiles(t *testing.T) (func(*Deps), string) {
	dir := t.TempDir()
	for p, c := range map[string]string{
		"world/level.dat":           "level",
		"plugins/Chunky/config.yml": "language: en\n",
		"server.properties":         "motd=x\n",
	} {
		full := filepath.Join(dir, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h, err := files.NewHandler(dir, "tok")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return func(d *Deps) { d.Files = fakeFiles{addr: u.Host} }, dir
}

func TestFilesAndBackup(t *testing.T) {
	opt, dir := withFiles(t)
	e := newTestEnv(t, opt)
	e.createUser("alice", auth.RoleUser, false)
	e.createUser("bob", auth.RoleUser, false)
	a := e.login("alice")
	var srv serverJSON
	a.do("POST", "/api/servers", createReq("files", e.plan("Small"))).expect(http.StatusCreated).decode(&srv)
	base := "/api/servers/" + srv.ID.String()

	var list struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	a.do("GET", base+"/files?path=plugins/Chunky", nil).expect(http.StatusOK).decode(&list)
	if len(list.Entries) != 1 || list.Entries[0].Name != "config.yml" {
		t.Fatalf("list: %+v", list)
	}
	a.do("PUT", base+"/files/content", map[string]string{"path": "plugins/Chunky/config.yml", "content": "language: it\n"}).expect(http.StatusNoContent)
	var read struct {
		Content string `json:"content"`
	}
	a.do("GET", base+"/files/content?path=plugins/Chunky/config.yml", nil).expect(http.StatusOK).decode(&read)
	if read.Content != "language: it\n" {
		t.Fatalf("content: %q", read.Content)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "plugins/Chunky/config.yml")); string(b) != "language: it\n" {
		t.Fatal("file not written")
	}
	// Sidecar errors pass through.
	a.do("GET", base+"/files/content?path=../../etc/passwd", nil).expect(http.StatusBadRequest)

	// Other users cannot reach the files.
	b := e.login("bob")
	b.do("GET", base+"/files", nil).expect(http.StatusNotFound)
	b.do("GET", base+"/backup", nil).expect(http.StatusNotFound)
	b.do("PUT", base+"/files/content", map[string]string{"path": "server.properties", "content": "x"}).expect(http.StatusNotFound)

	// A running server is flushed before and resumed after the backup.
	e.deps.Mgr.SetStatus(srv.ID, k8s.Status{State: k8s.StateRunning})
	req, _ := http.NewRequest("GET", a.base+base+"/backup?scope=world", nil)
	res, err := a.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "files-world-") {
		t.Fatalf("backup: %d %v", res.StatusCode, res.Header)
	}
	gz, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	found := false
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "world/level.dat" {
			found = true
		}
	}
	if !found {
		t.Fatal("world missing from backup")
	}
	want := []string{"save-off", "save-all flush", "save-on"}
	for _, w := range want {
		select {
		case got := <-e.rconCmds:
			for got != w && (got == "list" || strings.HasPrefix(got, "whitelist") || strings.HasPrefix(got, "difficulty")) {
				got = <-e.rconCmds
			}
			if got != w {
				t.Fatalf("rcon order: got %q want %q", got, w)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("missing rcon %q", w)
		}
	}
	a.do("GET", base+"/backup?scope=etc", nil).expect(http.StatusBadRequest)
}

func TestAutoSleepDisabled(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	req := createReq("sleepy", e.plan("Small"))
	req["settings"] = map[string]any{"auto_sleep": true}
	if res := a.do("POST", "/api/servers", req).expect(http.StatusBadRequest); !strings.Contains(res.errorMsg(), "auto sleep") {
		t.Fatalf("message: %s", res.errorMsg())
	}
}
