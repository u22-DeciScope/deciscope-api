package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	authmiddleware "deciscope-core-api/internal/adapter/http/middleware"
	postgresrepository "deciscope-core-api/internal/adapter/repository/postgres"
	appauth "deciscope-core-api/internal/application/auth"
	appworkspace "deciscope-core-api/internal/application/workspace"
	"deciscope-core-api/internal/infrastructure/database"
)

type staticVerifier struct {
	identity *appauth.Identity
}

func (v staticVerifier) VerifyIDToken(context.Context, string) (*appauth.Identity, error) {
	return v.identity, nil
}

// TestServerEnforcesWorkspaceRolesEndToEnd は、実DB + 実ルーターに対して
// ワークスペース作成・即時メンバー追加・ロール別認可をHTTP経由で検証する。
func TestServerEnforcesWorkspaceRolesEndToEnd(t *testing.T) {
	baseURL := os.Getenv("DATABASE_TEST_URL")
	if baseURL == "" {
		t.Skip("DATABASE_TEST_URL is not set")
	}
	// postgres パッケージの契約テストが DATABASE_TEST_URL のDBを TRUNCATE するため、
	// 並列実行と衝突しないよう E2E 専用DBを使う。
	databaseURL := createE2EDatabase(t, baseURL)
	setRequiredTranscriptEnv(t)
	t.Setenv("DATABASE_URL", databaseURL)

	db, err := database.Open(context.Background(), database.Config{URL: databaseURL})
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(context.Background(), db); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}

	runtime, err := NewServerRuntime()
	if err != nil {
		t.Fatalf("NewServerRuntime() error = %v", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	viewerEmail := "e2e-viewer-" + suffix + "@example.com"
	ownerToken, ownerID := loginTestUser(t, db, "e2e-owner-"+suffix, "e2e-owner-"+suffix+"@example.com", "E2E Owner")
	viewerToken, _ := loginTestUser(t, db, "e2e-viewer-"+suffix, viewerEmail, "E2E Viewer")

	// /v1/auth/me は workspaces を null ではなく配列で返す。
	me := serveJSON(t, runtime.Handler, http.MethodGet, "/v1/auth/me", "", ownerToken)
	if me.Code != http.StatusOK {
		t.Fatalf("GET /v1/auth/me status = %d, body = %s", me.Code, me.Body.String())
	}
	var meBody struct {
		Workspaces *[]json.RawMessage `json:"workspaces"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &meBody); err != nil {
		t.Fatalf("decode /v1/auth/me: %v", err)
	}
	if meBody.Workspaces == nil {
		t.Fatalf("/v1/auth/me workspaces = null, want array: %s", me.Body.String())
	}

	// owner がワークスペースを作成できる。
	created := serveJSON(t, runtime.Handler, http.MethodPost, "/v1/workspaces",
		`{"name":"E2Eワークスペース","description":"E2E検証用"}`, ownerToken)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /v1/workspaces status = %d, body = %s", created.Code, created.Body.String())
	}
	var workspace struct {
		ID          string `json:"id"`
		Role        string `json:"role"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &workspace); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	if workspace.ID == "" || workspace.Role != "owner" || workspace.Description != "E2E検証用" {
		t.Fatalf("created workspace = %+v, want owner role and description", workspace)
	}
	base := "/v1/workspaces/" + workspace.ID

	// 非メンバーはワークスペース詳細へアクセスできない。
	forbidden := serveJSON(t, runtime.Handler, http.MethodGet, base+"/", "", viewerToken)
	if forbidden.Code != http.StatusNotFound {
		t.Fatalf("GET workspace as non-member status = %d, want 404", forbidden.Code)
	}

	// 招待は pending で作成され、レスポンスに token_hash を含まない。
	// (runtime はSMTP未設定 + development のため dev fallback でメールはログ出力になる)
	invited := serveJSON(t, runtime.Handler, http.MethodPost, base+"/invitations",
		fmt.Sprintf(`{"email":"placeholder-%s@example.com","role":"viewer"}`, suffix), ownerToken)
	if invited.Code != http.StatusCreated {
		t.Fatalf("POST invitations status = %d, body = %s", invited.Code, invited.Body.String())
	}
	if !strings.Contains(invited.Body.String(), `"status":"pending"`) {
		t.Fatalf("invitation should be pending: %s", invited.Body.String())
	}
	if strings.Contains(invited.Body.String(), "token_hash") || strings.Contains(invited.Body.String(), "tokenHash") {
		t.Fatalf("invitation response must not contain token hash: %s", invited.Body.String())
	}
	var placeholderInvitation struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(invited.Body.Bytes(), &placeholderInvitation); err != nil {
		t.Fatalf("decode invitation: %v", err)
	}

	// 招待一覧に pending が表示され、取り消しできる。
	list := serveJSON(t, runtime.Handler, http.MethodGet, base+"/invitations", "", ownerToken)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "placeholder-"+suffix+"@example.com") {
		t.Fatalf("GET invitations status = %d, body = %s", list.Code, list.Body.String())
	}
	revoked := serveJSON(t, runtime.Handler, http.MethodDelete, base+"/invitations/"+placeholderInvitation.ID, "", ownerToken)
	if revoked.Code != http.StatusNoContent {
		t.Fatalf("DELETE invitation status = %d, body = %s", revoked.Code, revoked.Body.String())
	}

	// viewer 宛の招待を作成し、メールに載る生tokenを captureMailer で取得する。
	// (HTTPレスポンスには生tokenが含まれないため、サービス層経由で作成する)
	mailer := &captureInvitationMailer{}
	invitationService := appworkspace.NewService(postgresrepository.NewAuthWorkspaceRepository(db), mailer, "http://localhost:5193")
	if _, err := invitationService.CreateInvitation(context.Background(), ownerID, "E2E Owner", workspace.ID, viewerEmail, "viewer"); err != nil {
		t.Fatalf("CreateInvitation(service) error = %v", err)
	}
	rawToken := strings.TrimPrefix(mailer.acceptURL, "http://localhost:5193/invitations/accept?token=")
	if rawToken == "" || rawToken == mailer.acceptURL {
		t.Fatalf("accept url = %q, cannot extract token", mailer.acceptURL)
	}

	// preview は未ログインでも取得でき、ワークスペース名と招待先メールを返す。
	preview := serveJSON(t, runtime.Handler, http.MethodGet, "/v1/invitations/preview?token="+rawToken, "", "")
	if preview.Code != http.StatusOK || !strings.Contains(preview.Body.String(), "E2Eワークスペース") {
		t.Fatalf("GET preview status = %d, body = %s", preview.Code, preview.Body.String())
	}

	// 招待先メールと一致しないユーザー (owner) は承諾できない (403)。
	mismatch := serveJSON(t, runtime.Handler, http.MethodPost, "/v1/invitations/accept",
		fmt.Sprintf(`{"token":%q}`, rawToken), ownerToken)
	if mismatch.Code != http.StatusForbidden {
		t.Fatalf("POST accept (mismatch) status = %d, body = %s", mismatch.Code, mismatch.Body.String())
	}

	// 招待先本人 (viewer) は承諾でき、メンバーになる。
	accepted := serveJSON(t, runtime.Handler, http.MethodPost, "/v1/invitations/accept",
		fmt.Sprintf(`{"token":%q}`, rawToken), viewerToken)
	if accepted.Code != http.StatusOK || !strings.Contains(accepted.Body.String(), workspace.ID) {
		t.Fatalf("POST accept status = %d, body = %s", accepted.Code, accepted.Body.String())
	}

	// accepted 済み token は再利用できない (409)。
	reused := serveJSON(t, runtime.Handler, http.MethodPost, "/v1/invitations/accept",
		fmt.Sprintf(`{"token":%q}`, rawToken), viewerToken)
	if reused.Code != http.StatusConflict {
		t.Fatalf("POST accept (reuse) status = %d, body = %s", reused.Code, reused.Body.String())
	}

	// メンバーになった viewer は閲覧できる。
	viewerGet := serveJSON(t, runtime.Handler, http.MethodGet, base+"/", "", viewerToken)
	if viewerGet.Code != http.StatusOK {
		t.Fatalf("GET workspace as viewer status = %d, body = %s", viewerGet.Code, viewerGet.Body.String())
	}
	viewerSessions := serveJSON(t, runtime.Handler, http.MethodGet, base+"/meeting-sessions", "", viewerToken)
	if viewerSessions.Code != http.StatusOK {
		t.Fatalf("GET meeting-sessions as viewer status = %d, body = %s", viewerSessions.Code, viewerSessions.Body.String())
	}

	// viewer は会議を作成できない(403)。
	viewerCreate := serveJSON(t, runtime.Handler, http.MethodPost, base+"/meeting-sessions",
		`{"join_url":"https://teams.microsoft.com/meet/e2e"}`, viewerToken)
	if viewerCreate.Code != http.StatusForbidden {
		t.Fatalf("POST meeting-sessions as viewer status = %d, want 403", viewerCreate.Code)
	}

	// viewer はワークスペース情報もロールも変更できない(403)。
	viewerPatch := serveJSON(t, runtime.Handler, http.MethodPatch, base+"/", `{"name":"乗っ取り"}`, viewerToken)
	if viewerPatch.Code != http.StatusForbidden {
		t.Fatalf("PATCH workspace as viewer status = %d, want 403", viewerPatch.Code)
	}

	// owner は名前と説明を更新できる。
	ownerPatch := serveJSON(t, runtime.Handler, http.MethodPatch, base+"/", `{"description":"更新後"}`, ownerToken)
	if ownerPatch.Code != http.StatusOK || !strings.Contains(ownerPatch.Body.String(), `"description":"更新後"`) {
		t.Fatalf("PATCH workspace as owner status = %d, body = %s", ownerPatch.Code, ownerPatch.Body.String())
	}

	// 誤参加ユーザーの削除: 削除後はそのワークスペースのAPIへアクセスできない。
	var viewerUserID string
	if err := db.QueryRow(`
		SELECT u.id FROM users u JOIN user_emails e ON e.user_id = u.id
		WHERE e.normalized_email = $1
	`, viewerEmail).Scan(&viewerUserID); err != nil {
		t.Fatalf("lookup viewer user id: %v", err)
	}
	removed := serveJSON(t, runtime.Handler, http.MethodDelete, base+"/members/"+viewerUserID, "", ownerToken)
	if removed.Code != http.StatusNoContent {
		t.Fatalf("DELETE member status = %d, body = %s", removed.Code, removed.Body.String())
	}
	afterRemoval := serveJSON(t, runtime.Handler, http.MethodGet, base+"/", "", viewerToken)
	if afterRemoval.Code != http.StatusNotFound {
		t.Fatalf("GET workspace after removal status = %d, want 404", afterRemoval.Code)
	}
	afterRemovalSessions := serveJSON(t, runtime.Handler, http.MethodGet, base+"/meeting-sessions", "", viewerToken)
	if afterRemovalSessions.Code != http.StatusNotFound {
		t.Fatalf("GET meeting-sessions after removal status = %d, want 404", afterRemovalSessions.Code)
	}
}

const e2eDatabaseName = "deciscope_app_e2e"

// createE2EDatabase は DATABASE_TEST_URL と同じサーバー上に E2E 専用DBを用意する。
func createE2EDatabase(t *testing.T, baseURL string) string {
	t.Helper()
	adminDB, err := database.Open(context.Background(), database.Config{URL: baseURL})
	if err != nil {
		t.Fatalf("database.Open(admin) error = %v", err)
	}
	defer adminDB.Close()
	if _, err := adminDB.Exec("CREATE DATABASE " + e2eDatabaseName); err != nil &&
		!strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "42P04") {
		t.Fatalf("create e2e database: %v", err)
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse DATABASE_TEST_URL: %v", err)
	}
	parsed.Path = "/" + e2eDatabaseName
	return parsed.String()
}

type captureInvitationMailer struct {
	acceptURL string
}

func (m *captureInvitationMailer) SendInvitation(_ context.Context, email appworkspace.InvitationEmail) error {
	m.acceptURL = email.AcceptURL
	return nil
}

func loginTestUser(t *testing.T, db *sql.DB, uid, email, name string) (string, string) {
	t.Helper()
	service := appauth.NewService(
		postgresrepository.NewAuthWorkspaceRepository(db),
		staticVerifier{identity: &appauth.Identity{UID: uid, Email: email, Name: name, EmailVerified: true, Provider: "password"}},
		time.Hour,
	)
	result, err := service.Login(context.Background(), "token")
	if err != nil {
		t.Fatalf("login test user %s: %v", email, err)
	}
	return result.Token, result.User.ID
}

func serveJSON(t *testing.T, handler http.Handler, method, path, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	request.AddCookie(&http.Cookie{Name: authmiddleware.SessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
