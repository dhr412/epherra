package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type FileMetadata struct {
	Token          string        `bson:"token" json:"token"`
	Filename       string        `bson:"filename" json:"filename"`
	FileType       string        `bson:"fileType" json:"fileType"`
	FileData       string        `bson:"fileData,omitempty" json:"-"`
	FileID         bson.ObjectID `bson:"fileId" json:"-"`
	AllowDownloads bool          `bson:"allowDownloads" json:"allowDownloads"`
	AllowCopying   bool          `bson:"allowCopying" json:"allowCopying"`
	UploadedAt     time.Time     `bson:"uploadedAt" json:"uploadedAt"`
	ExpiresAt      time.Time     `bson:"expiresAt" json:"expiresAt"`
	MaxViews       *int          `bson:"maxViews" json:"maxViews"`
	CurrentViews   int           `bson:"currentViews" json:"currentViews"`
	Status         string        `bson:"status" json:"status"`
}

type UploadRequest struct {
	Filename       string    `json:"filename"`
	FileType       string    `json:"fileType"`
	FileData       string    `json:"fileData"`
	AllowDownloads bool      `json:"allowDownloads"`
	AllowCopying   bool      `json:"allowCopying"`
	MaxViews       *int      `json:"maxViews"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

var (
	mongoClient *mongo.Client
	collection  *mongo.Collection
	bucket      *mongo.GridFSBucket
)

func corsHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req UploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	validTypes := []string{
		"application/pdf",
		"application/x-ipynb+json",

		// Text & markup
		"text/plain", "text/markdown", "text/html", "text/css", "text/x-latex",

		// Programming languages (browser-displayable as text)
		"text/javascript", "application/javascript", "text/x-jsx", "text/x-tsx",
		"text/x-python", "text/x-csrc", "text/x-c++src", "text/x-java-source",
		"text/x-go", "text/x-ruby", "text/x-php", "text/x-shellscript",
		"text/x-typescript", "text/x-rustsrc", "text/x-r", "text/x-powershell",

		// Images (natively viewable)
		"image/png", "image/jpeg", "image/gif", "image/webp", "image/svg+xml",

		// Videos (natively playable)
		"video/mp4", "video/webm", "video/ogg",
	}

	if !contains(validTypes, req.FileType) {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	fileBytes, err := base64.StdEncoding.DecodeString(req.FileData)
	if err != nil {
		http.Error(w, "Invalid file data", http.StatusBadRequest)
		return
	}

	// Check file size (1.5MB threshold)
	const maxInlineSize = 1.5 * 1024 * 1024 // 1.5MB
	token := uuid.New().String()

	metadata := FileMetadata{
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

func viewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(r.URL.Path, "/api/view/")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var metadata FileMetadata
	err := collection.FindOne(ctx, bson.M{"token": token}).Decode(&metadata)
	if err != nil {
		http.Error(w, "File not found or expired", http.StatusNotFound)
		return
	}

	if metadata.Status != "active" {
		http.Error(w, "File has expired", http.StatusGone)
		return
	}

	if time.Now().After(metadata.ExpiresAt) {
		collection.UpdateOne(ctx, bson.M{"token": token}, bson.M{"$set": bson.M{"status": "expired"}})
		http.Error(w, "File has expired", http.StatusGone)
		return
	}

	if metadata.MaxViews != nil && metadata.CurrentViews >= *metadata.MaxViews {
		collection.UpdateOne(ctx, bson.M{"token": token}, bson.M{"$set": bson.M{"status": "expired"}})
		http.Error(w, "View limit reached", http.StatusGone)
		return
	}

	update := bson.M{"$inc": bson.M{"currentViews": 1}}
	if metadata.MaxViews != nil && metadata.CurrentViews+1 >= *metadata.MaxViews {
		update = bson.M{
			"$inc": bson.M{"currentViews": 1},
			"$set": bson.M{"status": "expired"},
		}
	}
	collection.UpdateOne(ctx, bson.M{"token": token}, update)

	w.Header().Set("Content-Type", metadata.FileType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, metadata.Filename))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("X-Allow-Downloads", fmt.Sprintf("%t", metadata.AllowDownloads))
	w.Header().Set("X-Allow-Copying", fmt.Sprintf("%t", metadata.AllowCopying))

	if metadata.FileData != "" {
		fileData, _ := base64.StdEncoding.DecodeString(metadata.FileData)
		w.Write(fileData)
		return
	}

	downloadStream, err := bucket.OpenDownloadStream(ctx, metadata.FileID)
	if err != nil {
		http.Error(w, "Failed to retrieve file", http.StatusInternalServerError)
		return
	}
	defer downloadStream.Close()

	if _, err := io.Copy(w, downloadStream); err != nil {
		http.Error(w, "Failed to stream file", http.StatusInternalServerError)
		return
	}
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoURI := os.Getenv("MONGODB_URI")
	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err == nil {
		err = client.Ping(ctx, nil)
	}
	if err != nil {
		panic(err)
	}
	mongoClient = client

	indexModel := mongo.IndexModel{
		Keys:    bson.M{"expiresAt": 1},
		Options: options.Index().SetExpireAfterSeconds(0),
	}
	collection.Indexes().CreateOne(ctx, indexModel)

	db := client.Database("epherra")
	collection = db.Collection("files")

	bucket = db.GridFSBucket(options.GridFSBucket().SetName("fs"))

	http.HandleFunc("/api/upload", corsHandler(uploadHandler))
	http.HandleFunc("/api/view/", corsHandler(viewHandler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	fmt.Printf("Server running on port %s\n", port)
	http.ListenAndServe(":"+port, nil)
}
