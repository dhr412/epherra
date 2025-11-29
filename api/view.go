package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"epherra-api/shared"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "https://epherra.vercel.app")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Password-Hash")
}

func Handler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w)

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Method != "GET" && r.Method != "HEAD" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	collection, bucket, err := shared.GetDB()
	if err != nil {
		http.Error(w, "Database connection failed", http.StatusInternalServerError)
		return
	}

	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "Token required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ip := shared.GetClientIP(r)
	if err := shared.CheckRateLimit(ctx, ip, "view", 16, 1*time.Hour); err != nil {
		if err.Error() == "rate limit exceeded" {
			http.Error(w, "Rate limit exceeded: max 16 views per hour", http.StatusTooManyRequests)
		} else {
			http.Error(w, "Database error checking rate limit", http.StatusInternalServerError)
		}
		return
	}

	var metadata shared.FileMetadata
	err = collection.FindOne(ctx, bson.M{"token": token}).Decode(&metadata)
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

	if metadata.IsEncrypted {
		providedHash := r.Header.Get("X-Password-Hash")
		if providedHash == "" || providedHash != metadata.PasswordHash {
			http.Error(w, "Password required", http.StatusUnauthorized)
			return
		}
	}

	if r.Method == "HEAD" {
		w.Header().Set("Content-Type", metadata.FileType)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, metadata.Filename))
		w.Header().Set("X-Is-Encrypted", fmt.Sprintf("%t", metadata.IsEncrypted))
		w.Header().Set("X-Allow-Downloads", fmt.Sprintf("%t", metadata.AllowDownloads))
		w.Header().Set("X-Allow-Copying", fmt.Sprintf("%t", metadata.AllowCopying))
		w.WriteHeader(http.StatusOK)
		return
	}

	var fileBytes []byte
	if metadata.FileData != "" {
		decoded, err := base64.StdEncoding.DecodeString(metadata.FileData)
		if err != nil {
			http.Error(w, "Invalid file data", http.StatusInternalServerError)
			return
		}
		fileBytes = decoded
	} else {
		downloadStream, err := bucket.OpenDownloadStream(ctx, metadata.FileID)
		if err != nil {
			http.Error(w, "Failed to retrieve file", http.StatusInternalServerError)
			return
		}
		defer downloadStream.Close()

		data, err := io.ReadAll(downloadStream)
		if err != nil {
			http.Error(w, "Failed to read file from storage", http.StatusInternalServerError)
			return
		}
		fileBytes = data
	}

	finalContentType := metadata.FileType
	finalFilename := metadata.Filename
	finalBytes := fileBytes

	update := bson.M{"$inc": bson.M{"currentViews": 1}}
	if metadata.MaxViews != nil && metadata.CurrentViews+1 >= *metadata.MaxViews {
		update["$set"] = bson.M{"status": "expired"}
	}
	collection.UpdateOne(ctx, bson.M{"token": token}, update)

	w.Header().Set("Content-Type", finalContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, finalFilename))
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("X-Is-Encrypted", fmt.Sprintf("%t", metadata.IsEncrypted))
	w.Header().Set("X-Allow-Downloads", fmt.Sprintf("%t", metadata.AllowDownloads))
	w.Header().Set("X-Allow-Copying", fmt.Sprintf("%t", metadata.AllowCopying))

	if _, err := io.Copy(w, bytes.NewReader(finalBytes)); err != nil {
		http.Error(w, "Failed to stream file", http.StatusInternalServerError)
		return
	}
}
