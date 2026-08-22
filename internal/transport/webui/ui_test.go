package webui_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elsell/reqdb/internal/transport/webui"
)

func TestEmbeddedUI(t *testing.T) {
	server := httptest.NewServer(webui.Handler("1.2.3"))
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "/assets/app.js") {
		t.Fatalf("index response: status=%d body=%q", response.StatusCode, body)
	}
	if response.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("content security policy is missing")
	}
	if !strings.Contains(string(body), `id="version">v1.2.3`) {
		t.Fatal("UI does not contain the build version")
	}
	if strings.Contains(string(body), "software") || strings.Contains(string(body), "SWR-") {
		t.Fatal("UI contains the removed requirement level")
	}
	for _, expected := range []string{`class="project-control"`, `class="topbar-action"`, `href="#i-logout"`} {
		if !strings.Contains(string(body), expected) {
			t.Errorf("index does not contain %q", expected)
		}
	}

	response, err = http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	script := string(body)
	if response.StatusCode != http.StatusOK || !strings.Contains(script, `authenticatedFetch("/v1/events"`) {
		t.Fatalf("script response: status=%d body=%q", response.StatusCode, body)
	}
	if strings.Contains(script, "software") || strings.Contains(script, "SWR-") {
		t.Fatal("UI script contains the removed requirement level")
	}
	for _, expected := range []string{"tasksByRequirement", "renderEvents", "state.events.unshift", "selectTaskInTree", "state.collapsed = new Set", "statusMatches", "aria-pressed"} {
		if !strings.Contains(script, expected) {
			t.Errorf("script does not contain %q", expected)
		}
	}
}
