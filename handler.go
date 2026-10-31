package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// A single PUT cannot resume after a failure, so larger files would need
// resumable uploads.
const maxVideoBytes = 5 << 30

const hlsManifest = "manifest.m3u8"

type Server struct {
	store   *Store
	storage *Storage
	auth    *Auth
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/google", s.signIn)
	mux.HandleFunc("GET /auth/google/callback", s.signInCallback)
	mux.HandleFunc("POST /auth/logout", s.signOut)
	mux.HandleFunc("GET /me", s.me)
	mux.HandleFunc("POST /videos", s.createVideo)
	mux.HandleFunc("GET /videos", s.listVideos)
	mux.HandleFunc("GET /videos/{id}/stream", s.streamVideo)
	mux.HandleFunc("GET /videos/{id}/hls/{path...}", s.hlsFile)
	mux.Handle("GET /", http.FileServerFS(webFS()))
	return mux
}

type createVideoRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	SizeBytes   int64  `json:"size_bytes"`
}

type createVideoResponse struct {
	Video         *Video            `json:"video"`
	UploadURL     string            `json:"upload_url"`
	UploadHeaders map[string]string `json:"upload_headers"`
	ExpiresAt     time.Time         `json:"expires_at"`
}

type streamVideoResponse struct {
	StreamURL string    `json:"stream_url"`
	Format    string    `json:"format"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

func (r createVideoRequest) validate() string {
	switch {
	case strings.TrimSpace(r.Title) == "":
		return "title is required"
	case strings.TrimSpace(r.Filename) == "":
		return "filename is required"
	case !strings.HasPrefix(r.ContentType, "video/"):
		return "content_type must be a video type"
	case r.SizeBytes <= 0:
		return "size_bytes must be positive"
	case r.SizeBytes > maxVideoBytes:
		return "size_bytes exceeds the 5 GiB limit"
	}
	return ""
}

func (s *Server) createVideo(w http.ResponseWriter, r *http.Request) {
	user, err := s.currentUser(r)
	if errors.Is(err, errSignInRequired) {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		internalError(w, "load user", err)
		return
	}

	var req createVideoRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if msg := req.validate(); msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	v := &Video{
		ID:          uuid.New(),
		Title:       req.Title,
		Description: req.Description,
		Filename:    req.Filename,
		ContentType: req.ContentType,
		SizeBytes:   req.SizeBytes,
	}

	// Signing first avoids leaving a row behind for a video that can never be
	// uploaded.
	url, headers, expiresAt, err := s.storage.SignUpload("videos/"+v.ID.String(), v.ContentType, v.SizeBytes)
	if err != nil {
		internalError(w, "sign upload", err)
		return
	}
	if err := s.store.Create(r.Context(), v, user.ID); err != nil {
		internalError(w, "create video", err)
		return
	}
	writeJSON(w, http.StatusCreated, createVideoResponse{Video: v, UploadURL: url, UploadHeaders: headers, ExpiresAt: expiresAt})
}

func (s *Server) listVideos(w http.ResponseWriter, r *http.Request) {
	videos, err := s.store.List(r.Context())
	if err != nil {
		internalError(w, "list videos", err)
		return
	}
	writeJSON(w, http.StatusOK, videos)
}

// streamVideo returns the HLS playlist once transcoding has finished, and a
// signed URL for the original file until then.
func (s *Server) streamVideo(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound.Error())
		return
	}

	hls, err := s.storage.Exists(r.Context(), hlsKey(id, hlsManifest))
	if err != nil {
		internalError(w, "check hls manifest", err)
		return
	}
	if hls {
		writeJSON(w, http.StatusOK, streamVideoResponse{
			StreamURL: "/videos/" + id.String() + "/hls/" + hlsManifest,
			Format:    "hls",
		})
		return
	}

	key := "videos/" + id.String()
	uploaded, err := s.storage.Exists(r.Context(), key)
	if err != nil {
		internalError(w, "check video", err)
		return
	}
	if !uploaded {
		writeError(w, http.StatusNotFound, errNotFound.Error())
		return
	}
	url, expiresAt, err := s.storage.SignRead(key)
	if err != nil {
		internalError(w, "sign stream", err)
		return
	}
	writeJSON(w, http.StatusOK, streamVideoResponse{StreamURL: url, Format: "file", ExpiresAt: expiresAt})
}

// hlsFile serves playlists itself, because a player resolves their relative
// segment paths against the URL it fetched them from. A redirect would point
// those paths at unsigned bucket URLs. Segments redirect to a signed URL, so
// video bytes never pass through the API.
func (s *Server) hlsFile(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound.Error())
		return
	}
	key := hlsKey(id, r.PathValue("path"))

	if strings.HasSuffix(key, ".m3u8") {
		playlist, err := s.storage.Read(r.Context(), key)
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			internalError(w, "read playlist", err)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		if _, err := w.Write(playlist); err != nil {
			slog.Error("write playlist", "err", err)
		}
		return
	}

	url, _, err := s.storage.SignRead(key)
	if err != nil {
		internalError(w, "sign segment", err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func hlsKey(id uuid.UUID, path string) string {
	return "transcoded/" + id.String() + "/" + path
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("write response", "err", err)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func internalError(w http.ResponseWriter, op string, err error) {
	slog.Error(op, "err", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}
