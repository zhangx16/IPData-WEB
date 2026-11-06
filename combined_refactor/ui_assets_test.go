package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopAssetsEmbeddedAndNoReviewTooling(t *testing.T) {
	m, e := newManager(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	defer m.close()
	handler := api(m)
	for path, kind := range map[string]string{"/": "text/html", "/app.css": "text/css", "/app.js": "text/javascript"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), kind) {
			t.Fatalf("asset%s status%d", path, w.Code)
		}
		for _, tooling := range []string{"draft-kit", "DRAFT_KIT", "__select", "seedReference", "setDraftState"} {
			if strings.Contains(w.Body.String(), tooling) {
				t.Fatalf("review tooling in production %s", path)
			}
		}
	}
	for _, path := range []string{"/draft-kit.js", "/draft-runtime.js", "/selection.json", "/api/providers/dmit"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatalf("unintended asset %s", path)
		}
	}
}
