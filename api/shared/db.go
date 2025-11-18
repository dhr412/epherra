package shared

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"time"

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
	PasswordHash   string        `bson:"passwordHash" json:"-"`
	IsEncrypted    bool          `bson:"isEncrypted" json:"isEncrypted"`
}

var (
	mongoClient     *mongo.Client
	collection      *mongo.Collection
	bucket          *mongo.GridFSBucket
	connectionMutex sync.Mutex
)

func encodeMongoURI(rawURI string) string {
	re := regexp.MustCompile(`^(mongodb(?:\+srv)?://)([^:]+):([^@]+)@(.+)$`)
	matches := re.FindStringSubmatch(rawURI)

	if len(matches) != 5 {
		return rawURI
	}

	protocol := matches[1]
	username := matches[2]
	password := matches[3]
	hostAndParams := matches[4]

	encodedUsername := url.QueryEscape(username)
	encodedPassword := url.QueryEscape(password)

	return fmt.Sprintf("%s%s:%s@%s", protocol, encodedUsername, encodedPassword, hostAndParams)
}

func GetDB() (*mongo.Collection, *mongo.GridFSBucket, error) {
	connectionMutex.Lock()
	defer connectionMutex.Unlock()

	if mongoClient != nil {
		return collection, bucket, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	rawMongoURI := os.Getenv("MONGODB_URI")
	mongoURI := encodeMongoURI(rawMongoURI)

	client, err := mongo.Connect(options.Client().ApplyURI(mongoURI))
	if err != nil {
		return nil, nil, fmt.Errorf("connection failed: %w", err)
	}

	if err = client.Ping(ctx, nil); err != nil {
		return nil, nil, fmt.Errorf("ping failed: %w", err)
	}

	mongoClient = client
	db := client.Database("epherra")
	collection = db.Collection("files")
	bucket = db.GridFSBucket(options.GridFSBucket().SetName("fs"))

	return collection, bucket, nil
}

func Handler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusTeapot)
}
