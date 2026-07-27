package destinations

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/pcarpe4/bento/collector/core"
)

// mongodbDestination inserts each message's data as a document, with the
// collection metadata stored under "_meta".
type mongodbDestination struct {
	client     *mongo.Client
	collection *mongo.Collection
}

func init() {
	core.RegisterDestination("mongodb", newMongoDBDestination)
}

func newMongoDBDestination(cfg core.Fields) (core.Destination, error) {
	uri, err := cfg.RequiredString("url")
	if err != nil {
		return nil, err
	}
	database, err := cfg.RequiredString("database")
	if err != nil {
		return nil, err
	}
	collection, err := cfg.RequiredString("collection")
	if err != nil {
		return nil, err
	}

	opts := options.Client().ApplyURI(uri)
	if username := cfg.String("username", ""); username != "" {
		opts = opts.SetAuth(options.Credential{
			Username: username,
			Password: cfg.String("password", ""),
		})
	}

	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongodb: %w", err)
	}

	return &mongodbDestination{
		client:     client,
		collection: client.Database(database).Collection(collection),
	}, nil
}

func (m *mongodbDestination) Write(ctx context.Context, batch []core.Message) error {
	docs := make([]any, 0, len(batch))
	for _, msg := range batch {
		doc := make(map[string]any, len(msg.Data)+1)
		for k, v := range msg.Data {
			doc[k] = v
		}
		doc["_meta"] = msg.Meta
		docs = append(docs, doc)
	}
	if _, err := m.collection.InsertMany(ctx, docs); err != nil {
		return fmt.Errorf("mongodb insert failed: %w", err)
	}
	return nil
}

func (m *mongodbDestination) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}
