package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"epherra-api/shared"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type UploadRequest struct {
	Filename       string    `json:"filename"`
	FileType       string    `json:"fileType"`
	FileData       string    `json:"fileData"`
	AllowDownloads bool      `json:"allowDownloads"`
	AllowCopying   bool      `json:"allowCopying"`
	MaxViews       *int      `json:"maxViews"`
	ExpiresAt      time.Time `json:"expiresAt"`
	PasswordHash   string    `json:"passwordHash"`
}

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "https://epherra.vercel.app")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func Handler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 20*1024*1024)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ip := shared.GetClientIP(r)
	if err := shared.CheckRateLimit(ctx, ip, "upload", 5, 24*time.Hour); err != nil {
		if err.Error() == "rate limit exceeded" {
			http.Error(w, "Rate limit exceeded: max 5 uploads per 24 hours", http.StatusTooManyRequests)
		} else {
			http.Error(w, "Database error checking rate limit", http.StatusInternalServerError)
		}
		return
	}

	collection, bucket, err := shared.GetDB()
	if err != nil {
		http.Error(w, "Database connection failed", http.StatusInternalServerError)
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		if err.Error() == "http: request body too large" {
			http.Error(w, "File too large (max 20MB)", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Invalid request", http.StatusBadRequest)
		}
		return
	}

	validTypes := []string{
		"application/pdf",
		"application/x-ipynb+json",
		// Text & markup
		"text/plain", "text/markdown", "text/html", "text/css", "text/x-latex",
		// Programming languages
		"text/javascript", "application/javascript", "text/x-jsx", "text/x-tsx",
		"text/x-python", "text/x-csrc", "text/x-c++src", "text/x-java-source",
		"text/x-go", "text/x-ruby", "text/x-php", "text/x-shellscript",
		"text/x-typescript", "text/x-rustsrc", "text/x-r", "text/x-powershell",
		// Images
		"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml",
		// Videos
		"video/mp4", "video/webm", "video/ogg",
	}

	if !slices.Contains(validTypes, req.FileType) {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	fileBytes, err := base64.StdEncoding.DecodeString(req.FileData)
	if err != nil {
		http.Error(w, "Invalid file data", http.StatusBadRequest)
		return
	}

	const maxInlineSize = 1.5 * 1024 * 1024 // 1.5MB
	token := uuid.New().String()

	metadata := shared.FileMetadata{
		Token:          token,
		Filename:       req.Filename,
		FileType:       req.FileType,
		AllowDownloads: req.AllowDownloads,
		AllowCopying:   req.AllowCopying,
		UploadedAt:     time.Now(),
		ExpiresAt:      req.ExpiresAt,
		MaxViews:       req.MaxViews,
		CurrentViews:   0,
		Status:         "active",
	}

	if metadata.ExpiresAt.IsZero() {
		metadata.ExpiresAt = time.Now().Add(72 * time.Hour)
	}

	if metadata.MaxViews == nil {
		defaultMaxViews := 1
		metadata.MaxViews = &defaultMaxViews
	}

	if req.PasswordHash != "" {
		metadata.PasswordHash = req.PasswordHash
		metadata.IsEncrypted = true
	}

	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if len(fileBytes) <= maxInlineSize {
		metadata.FileData = req.FileData
	} else {
		fileID := bson.NewObjectID()
		uploadOpts := options.GridFSUpload().SetMetadata(bson.M{"contentType": req.FileType})
		uploadStream, err := bucket.OpenUploadStreamWithID(ctx, fileID, req.Filename, uploadOpts)
		if err != nil {
			http.Error(w, "Failed to create upload stream", http.StatusInternalServerError)
			return
		}
		defer uploadStream.Close()

		_, err = io.Copy(uploadStream, bytes.NewReader(fileBytes))
		if err != nil {
			http.Error(w, "Failed to upload file", http.StatusInternalServerError)
			return
		}

		metadata.FileID = fileID
	}

	_, err = collection.InsertOne(ctx, metadata)
	if err != nil {
		http.Error(w, "Failed to save metadata", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"token": token})
}
