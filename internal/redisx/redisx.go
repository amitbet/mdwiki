package redisx

import (
	"context"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// PubSub wraps Redis for Yjs cross-node fan-out.
type PubSub struct {
	Client *redis.Client
	ch     string // prefix
}

var unlockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end
`)

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

func (p *PubSub) Enqueue(ctx context.Context, queueKey string, payload []byte) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	return p.Client.LPush(ctx, queueKey, payload).Err()
}

func (p *PubSub) DequeueBlocking(ctx context.Context, queueKey string, timeout time.Duration) ([]byte, error) {
	if p == nil || p.Client == nil {
		return nil, errors.New("redis unavailable")
	}
	vals, err := p.Client.BRPop(ctx, timeout, queueKey).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) != 2 {
		return nil, errors.New("invalid brpop response")
	}
	return []byte(vals[1]), nil
}

func (p *PubSub) PublishResult(ctx context.Context, resultKey string, payload []byte, ttl time.Duration) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	pipe := p.Client.TxPipeline()
	pipe.LPush(ctx, resultKey, payload)
	pipe.Expire(ctx, resultKey, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (p *PubSub) WaitResult(ctx context.Context, resultKey string, timeout time.Duration) ([]byte, error) {
	if p == nil || p.Client == nil {
		return nil, errors.New("redis unavailable")
	}
	vals, err := p.Client.BRPop(ctx, timeout, resultKey).Result()
	if err != nil {
		return nil, err
	}
	if len(vals) != 2 {
		return nil, errors.New("invalid brpop response")
	}
	return []byte(vals[1]), nil
}

func (p *PubSub) TryAcquireLock(ctx context.Context, lockKey, owner string, ttl time.Duration) (bool, error) {
	if p == nil || p.Client == nil {
		return false, errors.New("redis unavailable")
	}
	return p.Client.SetNX(ctx, lockKey, owner, ttl).Result()
}

func (p *PubSub) ReleaseLock(ctx context.Context, lockKey, owner string) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	_, err := unlockScript.Run(ctx, p.Client, []string{lockKey}, owner).Result()
	return err
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
