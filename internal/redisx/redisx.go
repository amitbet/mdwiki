package redisx

import (
	"context"
	"log"

	"github.com/redis/go-redis/v9"
)

// PubSub wraps Redis for Yjs cross-node fan-out.
type PubSub struct {
	Client *redis.Client
	ch     string // prefix
}

func New(url string) (*PubSub, error) {
	if url == "" {
		return nil, nil
	}
	opt, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	c := redis.NewClient(opt)
	if err := c.Ping(context.Background()).Err(); err != nil {
		return nil, err
	}
	return &PubSub{Client: c, ch: "mdwiki:yjs:"}, nil
}

func (p *PubSub) Publish(room string, data []byte) {
	if p == nil || p.Client == nil {
		return
	}
	ctx := context.Background()
	if err := p.Client.Publish(ctx, p.ch+room, data).Err(); err != nil {
		log.Printf("redis publish: %v", err)
	}
}

// Subscribe starts listening; call from goroutine, send received to hub.
func (p *PubSub) Subscribe(ctx context.Context, room string, fn func([]byte)) error {
	if p == nil || p.Client == nil {
		return nil
	}
	sub := p.Client.Subscribe(ctx, p.ch+room)
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if msg != nil {
				fn([]byte(msg.Payload))
			}
		}
	}
}
