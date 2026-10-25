package web_test

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/ykakade/tollgate/internal/admin"
	"github.com/ykakade/tollgate/web"
)

// Check the actual embedded production bundle through the admin handler.
// Static files must load before login; API data must still require a token.
func TestEmbeddedDashboard(t *testing.T) {
	dashboard, err := web.Handler()
	if err != nil {
		t.Fatal(err)
	}
	h := admin.Handler(admin.Options{
		Metrics: http.NotFoundHandler(), Token: "test-admin",
		Dashboard: dashboard,
		API:       http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	})
	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		return w
	}
	index := get("/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), `id="root"`) {
		t.Fatalf("index: status %d, body %s", index.Code, index.Body.String())
	}
	if index.Header().Get("Content-Security-Policy") == "" || index.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index headers: %v", index.Header())
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^" ]+)"`).FindAllStringSubmatch(index.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("the production bundle must reference JavaScript and CSS")
	}
	for _, asset := range assets {
		w := get(asset[1])
		if w.Code != http.StatusOK || w.Body.Len() == 0 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("asset %s: status %d, headers %v", asset[1], w.Code, w.Header())
		}
	}
	if w := get("/admin/v1/info"); w.Code != http.StatusUnauthorized {
		t.Fatalf("API without token: status %d", w.Code)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/admin/v1/info", nil)
	r.Header.Set("Authorization", "Bearer test-admin")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("API with token: status %d", w.Code)
	}
	if w := get("/assets/absent.js"); w.Code != http.StatusNotFound {
		t.Fatalf("missing asset: status %d", w.Code)
	}
}
