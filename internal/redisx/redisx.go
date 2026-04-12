package redisx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Options struct {
	Enabled     bool
	URL         string
	Addrs       []string
	ClusterMode bool
	Username    string
	Password    string
}

// PubSub wraps Redis for cross-node websocket fan-out and git queueing.
type PubSub struct {
	Client redis.UniversalClient
	ch     string
	nodeID string
}

type wireMessage struct {
	NodeID   string `json:"node_id"`
	IsBinary bool   `json:"is_binary"`
	DataB64  string `json:"data_b64"`
}

var unlockScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("del", KEYS[1])
else
	return 0
end
`)

var renewScript = redis.NewScript(`
if redis.call("get", KEYS[1]) == ARGV[1] then
	return redis.call("pexpire", KEYS[1], ARGV[2])
else
	return 0
end
`)

var enqueueOnceScript = redis.NewScript(`
if redis.call("exists", KEYS[2]) == 1 then
	return 0
end
redis.call("set", KEYS[2], ARGV[1], "PX", ARGV[2])
redis.call("xadd", KEYS[1], "*", "payload", ARGV[3])
return 1
`)

func New(opts Options) (*PubSub, error) {
	if !opts.Enabled {
		return nil, nil
	}

	client, err := newClient(opts)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, err
	}

	return &PubSub{
		Client: client,
		ch:     "mdwiki:yjs:",
		nodeID: uuid.NewString(),
	}, nil
}

func newClient(opts Options) (redis.UniversalClient, error) {
	if strings.TrimSpace(opts.URL) != "" {
		parsed, err := redis.ParseURL(opts.URL)
		if err != nil {
			return nil, err
		}
		parsed.Username = firstNonEmpty(opts.Username, parsed.Username)
		parsed.Password = firstNonEmpty(opts.Password, parsed.Password)
		return redis.NewClient(parsed), nil
	}

	addrs := compact(opts.Addrs)
	if len(addrs) == 0 {
		return nil, errors.New("redis enabled but no MDWIKI_REDIS_URL or MDWIKI_REDIS_ADDRS configured")
	}

	return redis.NewUniversalClient(&redis.UniversalOptions{
		Addrs:         addrs,
		Username:      opts.Username,
		Password:      opts.Password,
		IsClusterMode: opts.ClusterMode || len(addrs) > 1,
	}), nil
}

func (p *PubSub) Publish(room string, data []byte, isBinary bool) {
	if p == nil || p.Client == nil {
		return
	}

	payload, err := json.Marshal(wireMessage{
		NodeID:   p.nodeID,
		IsBinary: isBinary,
		DataB64:  EncodeBytesBase64(data),
	})
	if err != nil {
		log.Printf("redis publish encode: %v", err)
		return
	}

	ctx := context.Background()
	if err := p.Client.Publish(ctx, p.ch+room, payload).Err(); err != nil {
		log.Printf("redis publish: %v", err)
	}
}

func (p *PubSub) Enqueue(ctx context.Context, queueKey string, payload []byte) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	return p.Client.LPush(ctx, queueKey, payload).Err()
}

func (p *PubSub) EnqueueStreamOnce(ctx context.Context, streamKey, jobKey string, statePayload []byte, stateTTL time.Duration, jobPayload []byte) (bool, error) {
	if p == nil || p.Client == nil {
		return false, errors.New("redis unavailable")
	}
	res, err := enqueueOnceScript.Run(ctx, p.Client, []string{streamKey, jobKey}, statePayload, stateTTL.Milliseconds(), jobPayload).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (p *PubSub) EnsureConsumerGroup(ctx context.Context, streamKey, group string) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	err := p.Client.XGroupCreateMkStream(ctx, streamKey, group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return err
	}
	return nil
}

func (p *PubSub) ReadGroup(ctx context.Context, streamKey, group, consumer string, block time.Duration, count int64) ([]redis.XMessage, error) {
	if p == nil || p.Client == nil {
		return nil, errors.New("redis unavailable")
	}
	streams, err := p.Client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{streamKey, ">"},
		Count:    count,
		Block:    block,
	}).Result()
	if err != nil {
		return nil, err
	}
	return flattenMessages(streams), nil
}

func (p *PubSub) AutoClaim(ctx context.Context, streamKey, group, consumer, start string, minIdle time.Duration, count int64) ([]redis.XMessage, string, error) {
	if p == nil || p.Client == nil {
		return nil, start, errors.New("redis unavailable")
	}
	msgs, next, err := p.Client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   streamKey,
		Group:    group,
		Consumer: consumer,
		MinIdle:  minIdle,
		Start:    start,
		Count:    count,
	}).Result()
	return msgs, next, err
}

func (p *PubSub) AckStream(ctx context.Context, streamKey, group string, messageIDs ...string) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	if len(messageIDs) == 0 {
		return nil
	}
	if err := p.Client.XAck(ctx, streamKey, group, messageIDs...).Err(); err != nil {
		return err
	}
	return p.Client.XDel(ctx, streamKey, messageIDs...).Err()
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

func (p *PubSub) RenewLock(ctx context.Context, lockKey, owner string, ttl time.Duration) (bool, error) {
	if p == nil || p.Client == nil {
		return false, errors.New("redis unavailable")
	}
	n, err := renewScript.Run(ctx, p.Client, []string{lockKey}, owner, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (p *PubSub) Set(ctx context.Context, key string, payload []byte, ttl time.Duration) error {
	if p == nil || p.Client == nil {
		return errors.New("redis unavailable")
	}
	return p.Client.Set(ctx, key, payload, ttl).Err()
}

func (p *PubSub) Get(ctx context.Context, key string) ([]byte, error) {
	if p == nil || p.Client == nil {
		return nil, errors.New("redis unavailable")
	}
	val, err := p.Client.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	return val, nil
}

// Subscribe starts listening for room messages from other mdwiki nodes.
func (p *PubSub) Subscribe(ctx context.Context, room string, fn func([]byte, bool)) error {
	if p == nil || p.Client == nil {
		return nil
	}

	sub := p.Client.Subscribe(ctx, p.ch+room)
	defer sub.Close()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return nil
			}
			if msg == nil {
				continue
			}
			var payload wireMessage
			if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
				log.Printf("redis subscribe decode: %v", err)
				continue
			}
			if payload.NodeID == p.nodeID {
				continue
			}
			data, err := DecodeBytesBase64(payload.DataB64)
			if err != nil {
				log.Printf("redis subscribe data decode: %v", err)
				continue
			}
			fn(data, payload.IsBinary)
		}
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func compact(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func flattenMessages(streams []redis.XStream) []redis.XMessage {
	var out []redis.XMessage
	for _, stream := range streams {
		out = append(out, stream.Messages...)
	}
	return out
}

func EncodeBytesBase64(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

func DecodeBytesBase64(data string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(data)
}
