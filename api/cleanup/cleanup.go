package handler

import (
	"context"
	"encoding/json"
	"epherra-api/shared"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	cronSecret := os.Getenv("CRON_SECRET")

	expectedAuth := "Bearer " + cronSecret
	if authHeader != expectedAuth {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	collection, bucket, err := shared.GetDB()
	if err != nil {
		http.Error(w, "Database connection failed", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cursor, err := collection.Find(ctx, bson.M{"status": "expired"})

	gridfsDeletedCount := 0
	inlineDeletedCount := 0

	if err == nil {
		defer cursor.Close(ctx)

		var expiredFiles []shared.FileMetadata
		if cursor.All(ctx, &expiredFiles) == nil {
			for _, file := range expiredFiles {
				if !file.FileID.IsZero() {
					err := bucket.Delete(ctx, file.FileID)
					if err == nil {
						gridfsDeletedCount++
					}
				} else if file.FileData != "" {
					inlineDeletedCount++
				}
			}
		}
	}

	result, err := collection.DeleteMany(ctx, bson.M{"status": "expired"})
	metadataDeletedCount := int64(0)
	if err == nil {
		metadataDeletedCount = result.DeletedCount
	}

	now := time.Now()
	collection.UpdateMany(ctx, bson.M{
		"status":    "active",
		"expiresAt": bson.M{"$lt": now},
	}, bson.M{
		"$set": bson.M{"status": "expired"},
	})

	collection.UpdateMany(ctx, bson.M{
		"status":   "active",
		"maxViews": bson.M{"$exists": true, "$ne": nil},
		"$expr":    bson.M{"$gte": []any{"$currentViews", "$maxViews"}},
	}, bson.M{
		"$set": bson.M{"status": "expired"},
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := map[string]any{
		"success":            true,
		"timestamp":          time.Now().Format(time.RFC3339),
		"gridfsFilesDeleted": gridfsDeletedCount,
		"inlineFilesDeleted": inlineDeletedCount,
		"metadataDeleted":    metadataDeletedCount,
		"totalFilesDeleted":  gridfsDeletedCount + inlineDeletedCount,
		"message": fmt.Sprintf("Cleanup complete: %d GridFS files, %d inline files, %d metadata records deleted",
			gridfsDeletedCount, inlineDeletedCount, metadataDeletedCount),
	}

	fmt.Printf("Cleanup completed at %s: GridFS=%d, Inline=%d, Metadata=%d\n",
		time.Now().Format(time.RFC3339), gridfsDeletedCount, inlineDeletedCount, metadataDeletedCount)

	json.NewEncoder(w).Encode(response)
}
