package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerProtectsWorkspaceAndMeetingAPIs(t *testing.T) {
	t.Setenv("SQLITE_PATH", "file:server_test?mode=memory&cache=shared")
	handler, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	for _, path := range []string{"/v1/workspaces", "/v1/workspaces/w_test/meetings", "/v1/meetings/m_test"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		handler.ServeHTTP(resp, req)
		if resp.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want %d", path, resp.Code, http.StatusUnauthorized)
		}
	}
}
