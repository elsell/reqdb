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
	server := httptest.NewServer(webui.Handler())
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

	response, err = http.Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `new EventSource("/v1/events")`) {
		t.Fatalf("script response: status=%d body=%q", response.StatusCode, body)
	}
}
