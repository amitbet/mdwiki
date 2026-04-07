package ws

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// MsgType control messages (JSON text frames).
const (
	MsgYjsUpdate      = "yjs"           // binary frame, not JSON
	MsgRequestState   = "request_state" // server asks peer to supply full state
	MsgStateBlob      = "state_blob"    // peer -> server -> joiner
	MsgNeedSync       = "need_sync"     // joiner announces need full sync
	MsgSyncFailedLock = "sync_lock"     // server: read-only until peer sync
	MsgSyncOK         = "sync_ok"       // server: editing allowed
)

// Control JSON message.
type Control struct {
	Type       string `json:"type"`
	RequestID  string `json:"request_id,omitempty"`
	ForClient  string `json:"for_client,omitempty"`
	FromClient string `json:"from_client,omitempty"`
	DataB64    string `json:"data_b64,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// Client is one websocket connection.
type Client struct {
	ID       string
	Room     string
	Conn     *websocket.Conn
	Send     chan []byte
	Hub      *Hub
	UserID   string // optional session id for contributor tracking
	ReadOnly bool
}

// Hub routes Yjs updates and control messages per room.
type Hub struct {
	mu         sync.RWMutex
	rooms      map[string]map[*Client]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan broadcastMsg
	redis      RedisPubSub // optional
}

type broadcastMsg struct {
	room    string
	data    []byte
	skip    *Client
	isBinary bool
}

// RedisPubSub optional interface.
type RedisPubSub interface {
	Publish(room string, data []byte)
}

// NewHub creates hub.
func NewHub(r RedisPubSub) *Hub {
	return &Hub{
		rooms:      make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan broadcastMsg, 256),
		redis:      r,
	}
}

// Run hub loop.
func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.mu.Lock()
			if h.rooms[c.Room] == nil {
				h.rooms[c.Room] = make(map[*Client]bool)
			}
			h.rooms[c.Room][c] = true
			n := len(h.rooms[c.Room])
			h.mu.Unlock()
			log.Printf("ws: client %s joined room %s (%d peers)", c.ID, c.Room, n)
			if n == 1 {
				_ = c.Conn.WriteJSON(Control{Type: MsgSyncOK})
				c.ReadOnly = false
			} else {
				_ = c.Conn.WriteJSON(Control{Type: MsgNeedSync})
				c.ReadOnly = true
				_ = c.Conn.WriteJSON(Control{Type: MsgSyncFailedLock, Reason: "wait for peer sync"})
			}

		case c := <-h.unregister:
			h.mu.Lock()
			if m, ok := h.rooms[c.Room]; ok {
				delete(m, c)
				if len(m) == 0 {
					delete(h.rooms, c.Room)
				}
			}
			h.mu.Unlock()
			close(c.Send)

		case b := <-h.broadcast:
			h.mu.RLock()
			clients := h.rooms[b.room]
			for cl := range clients {
				if b.skip != nil && cl == b.skip {
					continue
				}
				select {
				case cl.Send <- b.data:
				default:
				}
			}
			h.mu.RUnlock()
			if h.redis != nil {
				h.redis.Publish(b.room, b.data)
			}
		}
	}
}

// Register adds client (call before readPump).
func (h *Hub) Register(c *Client) {
	h.register <- c
}

// Unregister removes client.
func (h *Hub) Unregister(c *Client) {
	h.unregister <- c
}

// BroadcastYjs sends binary update to all in room except skip.
func (h *Hub) BroadcastYjs(room string, data []byte, skip *Client) {
	h.broadcast <- broadcastMsg{room: room, data: data, skip: skip, isBinary: true}
}

// TryPeerStateSync picks another client in room and asks for state blob for target.
func (h *Hub) TryPeerStateSync(room string, joiner *Client, maxAttempts int) {
	h.mu.RLock()
	var peers []*Client
	for c := range h.rooms[room] {
		if c != joiner {
			peers = append(peers, c)
		}
	}
	h.mu.RUnlock()
	if len(peers) == 0 {
		_ = joiner.Conn.WriteJSON(Control{Type: MsgSyncOK})
		joiner.ReadOnly = false
		return
	}
	reqID := uuid.NewString()
	attempted := 0
	for _, peer := range peers {
		if attempted >= maxAttempts {
			break
		}
		attempted++
		msg := Control{
			Type:      MsgRequestState,
			RequestID: reqID,
			ForClient: joiner.ID,
		}
		if err := peer.Conn.WriteJSON(msg); err != nil {
			continue
		}
		// Wait for state_blob would be async in production; here we rely on client responding
		_ = reqID
		break
	}
}

// ForwardStateBlob relays encoded Yjs state from peer to joiner.
func (h *Hub) ForwardStateBlob(room, joinerID string, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.rooms[room] {
		if c.ID == joinerID {
			_ = c.Conn.WriteMessage(websocket.BinaryMessage, data)
			c.ReadOnly = false
			_ = c.Conn.WriteJSON(Control{Type: MsgSyncOK})
			return
		}
	}
}

// HandleStateBlobMessage decodes JSON control for state relay.
func HandleStateBlobPayload(joinerID string, dataB64 string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(dataB64)
}

// MarshalControl helper.
func MarshalControl(c Control) ([]byte, error) {
	return json.Marshal(c)
}
