// Package net implements the WebSocket streaming server for Alpha Network.
// Real-time block, transaction, and agent events are broadcast to connected clients.
package net

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/alpha-network/alpha/chain/core"
)

// AgentEvent represents a real-time agent lifecycle event (register, slash, reward, etc.)
type AgentEvent struct {
	Type    string          `json:"type"`    // "register", "slash", "reward", "task_complete"
	AgentID core.AgentID   `json:"agent_id"`
	Payload interface{}     `json:"payload,omitempty"`
	At      int64           `json:"at"` // unix timestamp
}

// wsMessage is the envelope for all WebSocket messages
type wsMessage struct {
	Type string      `json:"type"` // "block", "tx", "agent"
	Data interface{} `json:"data"`
}

// client represents a single connected WebSocket client
type client struct {
	conn   *websocket.Conn
	send   chan []byte
	hub    *Hub
	closeOnce sync.Once
}

// Hub manages all connected WebSocket clients and broadcast channels.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}

	broadcast  chan []byte
	register   chan *client
	unregister chan *client

	upgrader websocket.Upgrader

	stopOnce sync.Once
	stop     chan struct{}
}

// NewHub creates a new Hub ready to run.
func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*client]struct{}),
		broadcast:  make(chan []byte, 512),
		register:   make(chan *client, 64),
		unregister: make(chan *client, 64),
		stop:       make(chan struct{}),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 4096,
			CheckOrigin: func(r *http.Request) bool {
				return true // allow all origins (agents are API callers, not browsers)
			},
		},
	}
}

// Start launches the Hub's goroutine and begins listening for WebSocket
// connections on the given address at the /ws path.
// This method is non-blocking: the hub runs in the background.
func (h *Hub) Start(addr string) {
	go h.run()

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", h.ServeWS)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"clients": h.ClientCount(),
		})
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("Alpha Network WS hub listening on %s/ws", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("WS hub error: %v", err)
		}
	}()
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
// This handler can be mounted on any http.ServeMux, including the main API server.
func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WS upgrade error: %v", err)
		return
	}

	c := &client{
		conn: conn,
		send: make(chan []byte, 256),
		hub:  h,
	}
	h.register <- c

	go c.writePump()
	go c.readPump()
}

// BroadcastBlock serializes and broadcasts a new block to all connected clients.
func (h *Hub) BroadcastBlock(block *core.Block) {
	h.broadcastMessage(wsMessage{Type: "block", Data: block})
}

// BroadcastTx serializes and broadcasts a transaction to all connected clients.
func (h *Hub) BroadcastTx(tx *core.Transaction) {
	h.broadcastMessage(wsMessage{Type: "tx", Data: tx})
}

// BroadcastAgentEvent serializes and broadcasts an agent event to all connected clients.
func (h *Hub) BroadcastAgentEvent(event AgentEvent) {
	if event.At == 0 {
		event.At = time.Now().Unix()
	}
	h.broadcastMessage(wsMessage{Type: "agent", Data: event})
}

// ClientCount returns the number of currently connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// Stop gracefully shuts down the hub (best-effort, no forced close).
func (h *Hub) Stop() {
	h.stopOnce.Do(func() {
		close(h.stop)
	})
}

// broadcastMessage marshals the message and sends it to all clients.
func (h *Hub) broadcastMessage(msg wsMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("WS marshal error: %v", err)
		return
	}
	select {
	case h.broadcast <- data:
	default:
		// Broadcast channel full; drop message to avoid blocking producer
		log.Printf("WS broadcast channel full, dropping message type=%s", msg.Type)
	}
}

// run is the hub's central event loop.
func (h *Hub) run() {
	for {
		select {
		case <-h.stop:
			return

		case c := <-h.register:
			h.mu.Lock()
			h.clients[c] = struct{}{}
			h.mu.Unlock()
			log.Printf("WS client connected (total: %d)", h.ClientCount())

		case c := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				h.mu.Unlock()
				c.closeOnce.Do(func() {
					close(c.send)
					c.conn.Close()
				})
				log.Printf("WS client disconnected (total: %d)", h.ClientCount())
			} else {
				h.mu.Unlock()
			}

		case data := <-h.broadcast:
			h.mu.RLock()
			clients := make([]*client, 0, len(h.clients))
			for c := range h.clients {
				clients = append(clients, c)
			}
			h.mu.RUnlock()

			for _, c := range clients {
				select {
				case c.send <- data:
				default:
					// Client send buffer full — remove it
					h.unregister <- c
				}
			}
		}
	}
}

// writePump pumps messages from c.send to the WebSocket connection.
// A ping is sent every 30 seconds to keep the connection alive.
func (c *client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.hub.unregister <- c
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// readPump reads messages from the WebSocket connection.
// Alpha Network is a push-only streaming API; clients don't send data,
// but we need to drain the read side to detect disconnections.
func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
	}()

	c.conn.SetReadLimit(512)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
				websocket.CloseNormalClosure,
			) {
				log.Printf("WS unexpected close: %v", err)
			}
			return
		}
	}
}
