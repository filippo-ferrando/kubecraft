package api

import (
	"context"
	"net/http"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/auth"
)

func TestModsAndPlugins(t *testing.T) {
	e := newTestEnv(t)
	e.createUser("alice", auth.RoleUser, false)
	a := e.login("alice")
	small := e.plan("Small")

	var search struct {
		Kind   string `json:"kind"`
		Result struct {
			Hits []struct {
				Slug string `json:"slug"`
			} `json:"hits"`
		} `json:"result"`
	}
	a.do("GET", "/api/catalog/search?type=PAPER&version=1.21.4&q=perms", nil).expect(http.StatusOK).decode(&search)
	if search.Kind != "plugin" || len(search.Result.Hits) != 1 {
		t.Fatalf("search: %+v", search)
	}
	a.do("GET", "/api/catalog/search?type=VANILLA", nil).expect(http.StatusBadRequest)
	a.do("GET", "/api/catalog/search?type=PAPER&version=bad%3B", nil).expect(http.StatusBadRequest)

	req := createReq("plugins", small)
	req["settings"] = map[string]any{"modrinth_projects": []string{"Vebnzrzj"}}
	var srv serverJSON
	a.do("POST", "/api/servers", req).expect(http.StatusCreated).decode(&srv)
	if len(srv.Settings.ModrinthProjects) != 1 || srv.Settings.ModrinthProjects[0] != "luckperms" {
		t.Fatalf("projects not normalized: %v", srv.Settings.ModrinthProjects)
	}
	sts, err := e.kube.AppsV1().StatefulSets("minecraft").Get(context.Background(), "mc-plugins", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v := envMapAPI(sts.Spec.Template.Spec.Containers[0].Env)["MODRINTH_PROJECTS"]; v != "luckperms" {
		t.Fatalf("MODRINTH_PROJECTS = %q", v)
	}

	bad := map[string][]string{"VANILLA": {"luckperms"}, "PAPER": {"lithium"}, "FABRIC": {"missing"}}
	for typ, refs := range bad {
		req := createReq("bad", small)
		req["type"] = typ
		req["settings"] = map[string]any{"modrinth_projects": refs}
		a.do("POST", "/api/servers", req).expect(http.StatusBadRequest)
	}

	// Adding a mod later needs a restart, the list is validated again.
	a.do("PATCH", "/api/servers/"+srv.ID.String(), map[string]any{"settings": map[string]any{"modrinth_projects": []string{"luckperms", "lithium"}}}).expect(http.StatusBadRequest)
	var projects []map[string]any
	a.do("GET", "/api/catalog/projects?ids=luckperms", nil).expect(http.StatusOK).decode(&projects)
	if len(projects) != 1 || projects[0]["title"] != "LuckPerms" {
		t.Fatalf("projects: %v", projects)
	}
	a.do("GET", "/api/catalog/projects?ids=bad%20id", nil).expect(http.StatusBadRequest)
}
