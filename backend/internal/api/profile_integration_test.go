//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"

	"hrlens/backend/internal/auth"
)

// multipartBody builds a form with the given fields and an optional file.
func multipartBody(t *testing.T, fields map[string]string, fileName, ctype string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	if fileName != "" {
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="file"; filename="`+fileName+`"`)
		h.Set("Content-Type", ctype)
		part, _ := w.CreatePart(h)
		part.Write(data)
	}
	w.Close()
	return &buf, w.FormDataContentType()
}

func TestIntegrationProfile(t *testing.T) {
	s := integrationServer(t)
	s.cfg.DataDir = t.TempDir()
	ctx := context.Background()

	// A throwaway user, removed at the end
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO users (employee_code, first_name, last_name, email, password_hash)
		VALUES ('TEST-PROFILE', 'Test', 'Profile', 'test-profile@example.invalid', 'x') RETURNING id`).Scan(&id)
	if err != nil {
		t.Skip("cannot create a test user:", err)
	}
	t.Cleanup(func() { s.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id) })
	me := currentUser{ID: id, Role: auth.User}

	run := func(h http.HandlerFunc, method, target, pathID string, body *bytes.Buffer, ctype string, out any) int {
		t.Helper()
		if body == nil {
			body = &bytes.Buffer{}
		}
		req := httptest.NewRequest(method, target, body)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		if pathID != "" {
			req.SetPathValue("id", pathID)
		}
		req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, me))
		rec := httptest.NewRecorder()
		h(rec, req)
		if out != nil && rec.Code < 300 {
			if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
				t.Fatalf("%s %s: %v\n%s", method, target, err, rec.Body.String())
			}
		}
		if rec.Code >= 300 {
			t.Logf("%s %s -> %d %s", method, target, rec.Code, rec.Body.String())
		}
		return rec.Code
	}

	var p profileResponse
	if code := run(s.getProfile, "GET", "/api/profile", "", nil, "", &p); code != 200 {
		t.Fatalf("get: %d", code)
	}
	if p.User.ID != id || p.HasPhoto || len(p.Documents) != 0 || p.Details.Relations == nil {
		t.Fatalf("unexpected empty profile: %+v", p)
	}

	// Details: bad then good
	bad := `{"primary":{"gender":"x"},"contact":{"workMail":"bad"},"address":{},"relations":[],"job":{"noticeMonths":13}}`
	if code := run(s.updateProfile, "PUT", "/api/profile", "", bytes.NewBufferString(bad), "application/json", nil); code != 422 {
		t.Errorf("bad details: %d", code)
	}
	good := `{"primary":{"displayName":"Tester","gender":"female","dateOfBirth":"1990-05-20","maritalStatus":"","pwd":false,"bloodGroup":"O+"},
		"contact":{"workMail":"T@Example.com","mobilePhone":"+91 98765 43210"},"address":{"current":"1 Main St"},
		"relations":[{"name":"Ravi","relation":"Father","phone":""}],
		"job":{"dateOfJoining":"2025-05-20","inProbation":true,"jobTitle":"Consultant","jobCategory":"professionals","noticeMonths":2,"workType":"permanent","employmentType":"full_time"}}`
	var saved profileDetails
	if code := run(s.updateProfile, "PUT", "/api/profile", "", bytes.NewBufferString(good), "application/json", &saved); code != 200 {
		t.Fatalf("good details: %d", code)
	}
	if saved.Contact.WorkMail != "t@example.com" || saved.Job.InProbation == nil || !*saved.Job.InProbation || len(saved.Relations) != 1 {
		t.Errorf("details not saved as expected: %+v", saved)
	}
	run(s.getProfile, "GET", "/api/profile", "", nil, "", &p)
	if p.Details.Primary.DisplayName != "Tester" || p.UpdatedAt == nil {
		t.Errorf("details not read back: %+v", p.Details.Primary)
	}

	// Documents
	body, ctype := multipartBody(t, map[string]string{"folder": "degree", "details": `{"degree":"B.E","branch":"CSE","yearOfJoining":"2008-06-01","yearOfCompletion":"2012-05-01","cgpa":"8.1","university":"Anna"}`},
		"degree.pdf", "application/pdf", []byte("%PDF-1.4 test"))
	var doc userDocument
	if code := run(s.createDocument, "POST", "/api/profile/documents", "", body, ctype, &doc); code != 200 {
		t.Fatalf("create document: %d", code)
	}
	if doc.FileName == nil || *doc.FileName != "degree.pdf" || doc.Size == nil || *doc.Size != 13 {
		t.Errorf("document file not recorded: %+v", doc)
	}
	body, ctype = multipartBody(t, map[string]string{"folder": "degree", "details": `{"degree":""}`}, "x.exe", "application/octet-stream", []byte("x"))
	if code := run(s.createDocument, "POST", "/api/profile/documents", "", body, ctype, nil); code != 422 {
		t.Errorf("bad document: %d", code)
	}
	if n, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, "profiles", "*", "*")); len(n) != 1 {
		t.Errorf("expected exactly one stored file, got %v", n)
	}

	docID := itoa(doc.ID)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/profile/documents/"+docID+"/file", nil)
	req.SetPathValue("id", docID)
	s.documentFile(rec, req.WithContext(context.WithValue(req.Context(), ctxKey{}, me)))
	if rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "%PDF") || rec.Header().Get("Content-Type") != "application/pdf" {
		t.Errorf("document file: %d %s %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	// Another user must not see it
	rec = httptest.NewRecorder()
	s.documentFile(rec, req.WithContext(context.WithValue(req.Context(), ctxKey{}, currentUser{ID: id + 1000000, Role: auth.SuperAdmin})))
	if rec.Code != 404 {
		t.Errorf("another user read the document: %d", rec.Code)
	}

	// Update keeps the file when none is sent
	body, ctype = multipartBody(t, map[string]string{"folder": "degree", "details": `{"degree":"M.E","branch":"CSE","yearOfJoining":"2008-06-01","yearOfCompletion":"2012-05-01","cgpa":"8.1","university":"Anna"}`}, "", "", nil)
	if code := run(s.updateDocument, "PUT", "/api/profile/documents/"+docID, docID, body, ctype, &doc); code != 200 || doc.FileName == nil {
		t.Errorf("update document: %d %+v", code, doc)
	}

	// Photo
	body, ctype = multipartBody(t, nil, "me.png", "image/png", []byte("\x89PNG fake"))
	if code := run(s.setProfilePhoto, "POST", "/api/profile/photo", "", body, ctype, nil); code != 200 {
		t.Fatalf("photo: %d", code)
	}
	rec = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/api/profile/photo", nil)
	s.profilePhoto(rec, req.WithContext(context.WithValue(req.Context(), ctxKey{}, me)))
	if rec.Code != 200 {
		t.Errorf("read photo: %d", rec.Code)
	}
	u, _ := s.findUser(ctx, id)
	if !u.HasPhoto {
		t.Error("hasPhoto not set on the user")
	}
	// Replacing removes the old file
	body, ctype = multipartBody(t, nil, "me2.png", "image/png", []byte("\x89PNG fake 2"))
	run(s.setProfilePhoto, "POST", "/api/profile/photo", "", body, ctype, nil)
	if n, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, "profiles", "*", "*")); len(n) != 2 {
		t.Errorf("expected document + one photo on disk, got %v", n)
	}
	if code := run(s.removeProfilePhoto, "DELETE", "/api/profile/photo", "", nil, "", nil); code != 204 {
		t.Errorf("remove photo: %d", code)
	}
	if code := run(s.deleteDocument, "DELETE", "/api/profile/documents/"+docID, docID, nil, "", nil); code != 204 {
		t.Errorf("delete document: %d", code)
	}
	if n, _ := filepath.Glob(filepath.Join(s.cfg.DataDir, "profiles", "*", "*")); len(n) != 0 {
		t.Errorf("files left on disk: %v", n)
	}
	run(s.getProfile, "GET", "/api/profile", "", nil, "", &p)
	if p.HasPhoto || len(p.Documents) != 0 {
		t.Errorf("profile still lists removed things: %+v", p)
	}

}
