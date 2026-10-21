package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
)

// ---------- Profile: the signed-in person's own page (user menu > Profile) ----------
//
// Everything here is about the signed-in user only. Work details that admins set on the
// Users page (name, email, team, department, branch, manager) are read from users and
// cannot be changed here; the rest lives in user_profiles.details as one JSON document.

const (
	maxDocumentBytes = 10 << 20 // one attached document
	maxPhotoBytes    = 5 << 20
	maxDocuments     = 50 // per folder
	maxRelations     = 10
)

var (
	documentTypes   = map[string]string{"application/pdf": ".pdf", "image/jpeg": ".jpg", "image/png": ".png"}
	photoTypes      = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/webp": ".webp"}
	documentFolders = map[string]bool{"degree": true, "experience": true, "identity": true}

	genders        = oneOf("", "male", "female", "other")
	maritalStatus  = oneOf("", "single", "married", "divorced", "widowed")
	bloodGroups    = oneOf("", "A+", "A-", "B+", "B-", "AB+", "AB-", "O+", "O-")
	workTypes      = oneOf("", "permanent", "contract", "intern", "consultant")
	employmentType = oneOf("", "full_time", "part_time", "intern", "contractor")
	jobCategories  = oneOf("", "professionals", "management", "support", "technical", "other")
	phonePattern   = regexp.MustCompile(`^\+?[0-9][0-9 -]{5,19}$`)
	datePattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func oneOf(values ...string) map[string]bool {
	m := make(map[string]bool, len(values))
	for _, v := range values {
		m[v] = true
	}
	return m
}

type profilePrimary struct {
	DisplayName   string `json:"displayName"`
	Gender        string `json:"gender"`
	DateOfBirth   string `json:"dateOfBirth"` // YYYY-MM-DD or ""
	MaritalStatus string `json:"maritalStatus"`
	PWD           *bool  `json:"pwd"` // person with disability; nil = not set
	BloodGroup    string `json:"bloodGroup"`
}

type profileContact struct {
	WorkMail       string `json:"workMail"`
	PersonalMail   string `json:"personalMail"`
	MobilePhone    string `json:"mobilePhone"`
	WorkPhone      string `json:"workPhone"`
	ResidencePhone string `json:"residencePhone"`
	Skype          string `json:"skype"`
}

type profileAddress struct {
	Current   string `json:"current"`
	Permanent string `json:"permanent"`
}

type profileRelation struct {
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Phone    string `json:"phone"`
}

type profileJob struct {
	DateOfJoining  string `json:"dateOfJoining"`
	InProbation    *bool  `json:"inProbation"`
	JobTitle       string `json:"jobTitle"`
	JobCategory    string `json:"jobCategory"`
	NoticeMonths   *int   `json:"noticeMonths"`
	WorkType       string `json:"workType"`
	EmploymentType string `json:"employmentType"`
	Practice       string `json:"practice"`
	LegalEntity    string `json:"legalEntity"`
}

// profileDetails is the JSON kept in user_profiles.details.
type profileDetails struct {
	Primary   profilePrimary    `json:"primary"`
	Contact   profileContact    `json:"contact"`
	Address   profileAddress    `json:"address"`
	Relations []profileRelation `json:"relations"`
	Job       profileJob        `json:"job"`
}

func text(errs map[string]string, key, value string, max int) string {
	v := strings.TrimSpace(value)
	if len(v) > max {
		errs[key] = fmt.Sprintf("Use at most %d characters", max)
	}
	return v
}

func optionalDate(errs map[string]string, key, value string) string {
	v := strings.TrimSpace(value)
	if v == "" {
		return ""
	}
	if _, err := time.Parse(time.DateOnly, v); err != nil || !datePattern.MatchString(v) {
		errs[key] = "Enter a date as YYYY-MM-DD"
	}
	return v
}

func optionalPhone(errs map[string]string, key, value string) string {
	v := strings.TrimSpace(value)
	if v != "" && !phonePattern.MatchString(v) {
		errs[key] = "Enter a phone number with 6 to 20 digits"
	}
	return v
}

func optionalEmail(errs map[string]string, key, value string) string {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return ""
	}
	parsed, err := mail.ParseAddress(v)
	_, domain, _ := strings.Cut(v, "@")
	if err != nil || parsed.Address != v || !strings.Contains(domain, ".") || len(v) > 254 {
		errs[key] = "Enter a valid email address"
	}
	return v
}

func choice(errs map[string]string, key, value string, allowed map[string]bool) string {
	v := strings.TrimSpace(value)
	if !allowed[v] {
		errs[key] = "Choose one of the options"
	}
	return v
}

// validate tidies the details and returns field errors keyed like the form (section.field).
func (d *profileDetails) validate() map[string]string {
	errs := map[string]string{}
	p := &d.Primary
	p.DisplayName = text(errs, "primary.displayName", p.DisplayName, 100)
	p.Gender = choice(errs, "primary.gender", p.Gender, genders)
	p.DateOfBirth = optionalDate(errs, "primary.dateOfBirth", p.DateOfBirth)
	if p.DateOfBirth != "" && errs["primary.dateOfBirth"] == "" {
		if dob, _ := time.Parse(time.DateOnly, p.DateOfBirth); dob.After(time.Now()) {
			errs["primary.dateOfBirth"] = "Date of birth cannot be in the future"
		}
	}
	p.MaritalStatus = choice(errs, "primary.maritalStatus", p.MaritalStatus, maritalStatus)
	p.BloodGroup = choice(errs, "primary.bloodGroup", p.BloodGroup, bloodGroups)

	c := &d.Contact
	c.WorkMail = optionalEmail(errs, "contact.workMail", c.WorkMail)
	c.PersonalMail = optionalEmail(errs, "contact.personalMail", c.PersonalMail)
	c.MobilePhone = optionalPhone(errs, "contact.mobilePhone", c.MobilePhone)
	c.WorkPhone = optionalPhone(errs, "contact.workPhone", c.WorkPhone)
	c.ResidencePhone = optionalPhone(errs, "contact.residencePhone", c.ResidencePhone)
	c.Skype = text(errs, "contact.skype", c.Skype, 100)

	d.Address.Current = text(errs, "address.current", d.Address.Current, 500)
	d.Address.Permanent = text(errs, "address.permanent", d.Address.Permanent, 500)

	if len(d.Relations) > maxRelations {
		errs["relations"] = fmt.Sprintf("Add at most %d relations", maxRelations)
	}
	clean := make([]profileRelation, 0, len(d.Relations))
	for i, rel := range d.Relations {
		key := "relations." + strconv.Itoa(i)
		rel.Name = text(errs, key+".name", rel.Name, 100)
		rel.Relation = text(errs, key+".relation", rel.Relation, 50)
		rel.Phone = optionalPhone(errs, key+".phone", rel.Phone)
		if rel.Name == "" {
			errs[key+".name"] = "Name is required"
		}
		if rel.Relation == "" {
			errs[key+".relation"] = "Relation is required"
		}
		clean = append(clean, rel)
	}
	d.Relations = clean

	j := &d.Job
	j.DateOfJoining = optionalDate(errs, "job.dateOfJoining", j.DateOfJoining)
	j.JobTitle = text(errs, "job.jobTitle", j.JobTitle, 100)
	j.JobCategory = choice(errs, "job.jobCategory", j.JobCategory, jobCategories)
	if j.NoticeMonths != nil && (*j.NoticeMonths < 0 || *j.NoticeMonths > 12) {
		errs["job.noticeMonths"] = "Enter 0 to 12 months"
	}
	j.WorkType = choice(errs, "job.workType", j.WorkType, workTypes)
	j.EmploymentType = choice(errs, "job.employmentType", j.EmploymentType, employmentType)
	j.Practice = text(errs, "job.practice", j.Practice, 100)
	j.LegalEntity = text(errs, "job.legalEntity", j.LegalEntity, 100)
	return errs
}

type userDocument struct {
	ID          int64           `json:"id"`
	Folder      string          `json:"folder"`
	Details     json.RawMessage `json:"details"`
	FileName    *string         `json:"fileName"`
	ContentType *string         `json:"contentType"`
	Size        *int64          `json:"size"`
	CreatedAt   time.Time       `json:"createdAt"`
	UpdatedAt   time.Time       `json:"updatedAt"`
}

type profileResponse struct {
	User          userJSON       `json:"user"`
	Details       profileDetails `json:"details"`
	HasPhoto      bool           `json:"hasPhoto"`
	UpdatedAt     *time.Time     `json:"updatedAt"`
	JoinedAt      time.Time      `json:"joinedAt"` // account created
	DirectReports []ref          `json:"directReports"`
	Documents     []userDocument `json:"documents"`
}

func (s *Server) loadDetails(r *http.Request, userID int64) (profileDetails, bool, *time.Time, error) {
	var d profileDetails
	var photo *string
	var updated *time.Time
	err := s.db.QueryRow(r.Context(), `SELECT details, photo_path, updated_at FROM user_profiles WHERE user_id = $1`, userID).
		Scan(&d, &photo, &updated)
	if errors.Is(err, pgx.ErrNoRows) {
		err = nil
	}
	if d.Relations == nil {
		d.Relations = []profileRelation{}
	}
	return d, photo != nil, updated, err
}

func (s *Server) listDocuments(r *http.Request, userID int64) ([]userDocument, error) {
	rows, err := s.db.Query(r.Context(), `
		SELECT id, folder, details, file_name, content_type, size, created_at, updated_at
		FROM user_documents WHERE user_id = $1 ORDER BY folder, id`, userID)
	if err != nil {
		return nil, err
	}
	docs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (userDocument, error) {
		var d userDocument
		err := row.Scan(&d.ID, &d.Folder, &d.Details, &d.FileName, &d.ContentType, &d.Size, &d.CreatedAt, &d.UpdatedAt)
		return d, err
	})
	if docs == nil {
		docs = []userDocument{}
	}
	return docs, err
}

// GET /api/profile
func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	u, err := s.findUser(r.Context(), me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	details, hasPhoto, updated, err := s.loadDetails(r, me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var joined time.Time
	if err := s.db.QueryRow(r.Context(), `SELECT created_at FROM users WHERE id = $1`, me.ID).Scan(&joined); err != nil {
		s.fail(w, r, err)
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT id, trim(first_name || ' ' || last_name) FROM users
		WHERE manager_id = $1 AND status = 'active' ORDER BY lower(first_name), lower(last_name)`, me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	reports, err := pgx.CollectRows(rows, pgx.RowToStructByPos[ref])
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if reports == nil {
		reports = []ref{}
	}
	docs, err := s.listDocuments(r, me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, profileResponse{
		User: u, Details: details, HasPhoto: hasPhoto, UpdatedAt: updated, JoinedAt: joined,
		DirectReports: reports, Documents: docs,
	})
}

// PUT /api/profile — the whole details document
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	var in profileDetails
	if !decode(w, r, &in) {
		return
	}
	if errs := in.validate(); len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}
	me := userFrom(r)
	_, err := s.db.Exec(r.Context(), `
		INSERT INTO user_profiles (user_id, details) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET details = EXCLUDED.details, updated_at = now()`, me.ID, in)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

// storeUpload copies one multipart file under DATA_DIR/profiles/<user>/ and returns its relative path.
func (s *Server) storeUpload(userID int64, file io.Reader, ext string, limit int64) (rel string, size int64, err error) {
	suffix, _, err := auth.NewToken()
	if err != nil {
		return "", 0, err
	}
	rel = filepath.Join("profiles", strconv.FormatInt(userID, 10), time.Now().UTC().Format("20060102-150405")+"-"+suffix[:10]+ext)
	full := filepath.Join(s.cfg.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return "", 0, err
	}
	size, err = io.Copy(out, io.LimitReader(file, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && size > limit {
		err = errTooLarge
	}
	if err != nil {
		os.Remove(full)
		return "", 0, err
	}
	return filepath.ToSlash(rel), size, nil
}

var errTooLarge = errors.New("file too large")

func (s *Server) removeFile(rel *string) {
	if rel == nil || *rel == "" {
		return
	}
	if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(*rel))); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.log.Warn("could not remove file", "path", *rel, "error", err)
	}
}

// POST /api/profile/photo — multipart "file"
func (s *Server) setProfilePhoto(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Choose an image up to 5 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Choose an image")
		return
	}
	defer file.Close()
	ext, ok := photoTypes[header.Header.Get("Content-Type")]
	if !ok {
		writeError(w, http.StatusBadRequest, "Use a JPG, PNG or WebP image")
		return
	}
	rel, _, err := s.storeUpload(me.ID, file, ext, maxPhotoBytes)
	if errors.Is(err, errTooLarge) {
		writeError(w, http.StatusBadRequest, "Choose an image up to 5 MB")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	var old *string
	err = s.db.QueryRow(r.Context(), `
		WITH prev AS (SELECT photo_path FROM user_profiles WHERE user_id = $1)
		INSERT INTO user_profiles (user_id, photo_path) VALUES ($1, $2)
		ON CONFLICT (user_id) DO UPDATE SET photo_path = EXCLUDED.photo_path, updated_at = now()
		RETURNING (SELECT photo_path FROM prev)`, me.ID, rel).Scan(&old)
	if err != nil {
		s.removeFile(&rel)
		s.fail(w, r, err)
		return
	}
	s.removeFile(old)
	writeJSON(w, http.StatusOK, map[string]bool{"hasPhoto": true})
}

// DELETE /api/profile/photo
func (s *Server) removeProfilePhoto(w http.ResponseWriter, r *http.Request) {
	me := userFrom(r)
	var old *string
	err := s.db.QueryRow(r.Context(), `
		WITH prev AS (SELECT photo_path FROM user_profiles WHERE user_id = $1)
		UPDATE user_profiles SET photo_path = NULL, updated_at = now() WHERE user_id = $1 AND photo_path IS NOT NULL
		RETURNING (SELECT photo_path FROM prev)`, me.ID).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	s.removeFile(old)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/profile/photo — the signed-in person's photo; GET /api/users/{id}/photo for anyone
func (s *Server) profilePhoto(w http.ResponseWriter, r *http.Request) {
	userID := userFrom(r).ID
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		userID = id
	}
	me := userFrom(r)
	var path *string
	var at time.Time
	err := s.db.QueryRow(r.Context(), `
		SELECT p.photo_path, p.updated_at FROM user_profiles p JOIN users u ON u.id = p.user_id
		WHERE p.user_id = $3 AND `+visiblePeople, string(me.Role), me.ID, userID).Scan(&path, &at)
	if err != nil || path == nil {
		writeError(w, http.StatusNotFound, "No photo")
		return
	}
	s.serveStored(w, r, *path, "", at)
}

func (s *Server) serveStored(w http.ResponseWriter, r *http.Request, rel, name string, at time.Time) {
	file, err := os.Open(filepath.Join(s.cfg.DataDir, filepath.FromSlash(rel)))
	if err != nil {
		writeError(w, http.StatusNotFound, "The file is no longer stored")
		return
	}
	defer file.Close()
	h := w.Header()
	h.Set("Cache-Control", "private, no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	if name == "" {
		name = filepath.Base(rel)
	}
	http.ServeContent(w, r, name, at, file)
}

// POST /api/profile/documents — multipart: folder, details (JSON), file (optional)
func (s *Server) createDocument(w http.ResponseWriter, r *http.Request) {
	s.saveDocument(w, r, 0)
}

// PUT /api/profile/documents/{id} — same body; a new file replaces the old one
func (s *Server) updateDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	s.saveDocument(w, r, id)
}

// Fields each folder keeps, with their labels for error messages. Dates are YYYY-MM-DD.
var documentFields = map[string][]struct {
	Key, Label string
	Required   bool
	Kind       string // text | date | number
	Max        int
}{
	"degree": {
		{"degree", "Degree", true, "text", 100}, {"branch", "Branch/Specialization", true, "text", 100},
		{"yearOfJoining", "Year of joining", true, "date", 0}, {"yearOfCompletion", "Year of completion", true, "date", 0},
		{"cgpa", "CGPA/Percentage", true, "text", 20}, {"university", "University/College", true, "text", 200},
	},
	"experience": {
		{"company", "Company", true, "text", 200}, {"designation", "Designation", true, "text", 100},
		{"from", "From", true, "date", 0}, {"to", "To", false, "date", 0},
		{"location", "Location", false, "text", 100}, {"description", "Description", false, "text", 1000},
	},
	"identity": {
		{"documentType", "Document type", true, "text", 50}, {"number", "Document number", true, "text", 50},
		{"issuedOn", "Issued on", false, "date", 0}, {"expiresOn", "Expires on", false, "date", 0},
		{"issuedBy", "Issued by", false, "text", 100},
	},
}

func validateDocument(folder string, raw map[string]any) (map[string]string, map[string]string) {
	errs := map[string]string{}
	clean := map[string]string{}
	for _, f := range documentFields[folder] {
		v, _ := raw[f.Key].(string)
		v = strings.TrimSpace(v)
		switch {
		case v == "" && f.Required:
			errs[f.Key] = f.Label + " is required"
		case v == "":
		case f.Kind == "date":
			v = optionalDate(errs, f.Key, v)
		case f.Max > 0 && len(v) > f.Max:
			errs[f.Key] = fmt.Sprintf("Use at most %d characters", f.Max)
		}
		clean[f.Key] = v
	}
	if folder == "degree" && errs["yearOfJoining"] == "" && errs["yearOfCompletion"] == "" && clean["yearOfCompletion"] < clean["yearOfJoining"] {
		errs["yearOfCompletion"] = "Completion must be after joining"
	}
	if folder == "experience" && clean["to"] != "" && errs["from"] == "" && errs["to"] == "" && clean["to"] < clean["from"] {
		errs["to"] = "End must be after the start"
	}
	return clean, errs
}

func (s *Server) saveDocument(w http.ResponseWriter, r *http.Request, id int64) {
	me := userFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes+1<<20)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Attach a file up to 10 MB")
		return
	}
	defer r.MultipartForm.RemoveAll()

	folder := r.FormValue("folder")
	if !documentFolders[folder] {
		writeError(w, http.StatusBadRequest, "Unknown document folder")
		return
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.FormValue("details")), &raw); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid details")
		return
	}
	clean, errs := validateDocument(folder, raw)

	var rel, name, ctype *string
	var size *int64
	file, header, err := r.FormFile("file")
	switch {
	case errors.Is(err, http.ErrMissingFile):
	case err != nil:
		writeError(w, http.StatusBadRequest, "Could not read the attached file")
		return
	default:
		defer file.Close()
		ext, ok := documentTypes[header.Header.Get("Content-Type")]
		if !ok {
			errs["file"] = "Allowed files: PDF, JPG, JPEG, PNG"
			break
		}
		if len(errs) > 0 {
			break
		}
		p, n, err := s.storeUpload(me.ID, file, ext, maxDocumentBytes)
		if errors.Is(err, errTooLarge) {
			errs["file"] = "Attach a file up to 10 MB"
			break
		}
		if err != nil {
			s.fail(w, r, err)
			return
		}
		fn, ct := filepath.Base(header.Filename), header.Header.Get("Content-Type")
		rel, name, ctype, size = &p, &fn, &ct, &n
	}
	if len(errs) > 0 {
		writeFieldErrors(w, errs)
		return
	}

	if id == 0 {
		var count int
		if err := s.db.QueryRow(r.Context(), `SELECT count(*) FROM user_documents WHERE user_id = $1 AND folder = $2`, me.ID, folder).Scan(&count); err != nil {
			s.removeFile(rel)
			s.fail(w, r, err)
			return
		}
		if count >= maxDocuments {
			s.removeFile(rel)
			writeError(w, http.StatusBadRequest, fmt.Sprintf("This folder holds at most %d entries", maxDocuments))
			return
		}
		err = s.db.QueryRow(r.Context(), `
			INSERT INTO user_documents (user_id, folder, details, file_name, file_path, content_type, size)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, me.ID, folder, clean, name, rel, ctype, size).Scan(&id)
		if err != nil {
			s.removeFile(rel)
			s.fail(w, r, err)
			return
		}
	} else {
		var old *string
		err = s.db.QueryRow(r.Context(), `
			WITH prev AS (SELECT file_path FROM user_documents WHERE id = $1)
			UPDATE user_documents SET details = $3,
			       file_name = COALESCE($4, file_name), file_path = COALESCE($5, file_path),
			       content_type = COALESCE($6, content_type), size = COALESCE($7, size), updated_at = now()
			WHERE id = $1 AND user_id = $2 AND folder = $8
			RETURNING (SELECT file_path FROM prev)`,
			id, me.ID, clean, name, rel, ctype, size, folder).Scan(&old)
		if errors.Is(err, pgx.ErrNoRows) {
			s.removeFile(rel)
			writeError(w, http.StatusNotFound, "Document not found")
			return
		}
		if err != nil {
			s.removeFile(rel)
			s.fail(w, r, err)
			return
		}
		if rel != nil {
			s.removeFile(old)
		}
	}
	docs, err := s.listDocuments(r, me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, d := range docs {
		if d.ID == id {
			writeJSON(w, http.StatusOK, d)
			return
		}
	}
	writeError(w, http.StatusNotFound, "Document not found")
}

// DELETE /api/profile/documents/{id}
func (s *Server) deleteDocument(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var path *string
	err := s.db.QueryRow(r.Context(), `DELETE FROM user_documents WHERE id = $1 AND user_id = $2 RETURNING file_path`, id, userFrom(r).ID).Scan(&path)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "Document not found")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.removeFile(path)
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/profile/documents/{id}/file
func (s *Server) documentFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var path, name, ctype *string
	var at time.Time
	err := s.db.QueryRow(r.Context(), `SELECT file_path, file_name, content_type, updated_at FROM user_documents WHERE id = $1 AND user_id = $2`,
		id, userFrom(r).ID).Scan(&path, &name, &ctype, &at)
	if err != nil || path == nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	w.Header().Set("Content-Type", *ctype)
	if r.URL.Query().Get("download") == "1" {
		w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.ReplaceAll(*name, "\"", "")+"\"")
	}
	s.serveStored(w, r, *path, *name, at)
}
