package main

import (
	"fmt"
	"log"
	"net/http"
	"sync"
)

// Client represents a connected SSE client
type Client struct {
	ID     string
	Events chan []byte // Each client needs its own buffer to receive events from the broadcaster
}

// Broadcaster manages SSE clients and broadcasts events
type Broadcaster struct {
	clients        map[*Client]bool
	clientsMutex   sync.RWMutex
	registerChan   chan *Client
	unregisterChan chan *Client
	broadcastChan  <-chan []byte // Input channel only
	shutdown       chan struct{}
	isRunning      bool
	runningMutex   sync.Mutex
}

// NewBroadcaster creates a new SSE broadcaster
// The broadcastChan parameter allows using an external channel as event source
func NewBroadcaster(broadcastChan <-chan []byte) *Broadcaster {
	return &Broadcaster{
		clients:        make(map[*Client]bool),
		clientsMutex:   sync.RWMutex{},
		registerChan:   make(chan *Client),
		unregisterChan: make(chan *Client),
		broadcastChan:  broadcastChan,
		shutdown:       make(chan struct{}),
		isRunning:      false,
	}
}

// Start begins the broadcaster's main loop
// If broadcastChan is nil in the constructor, you must provide it here
func (b *Broadcaster) Start(broadcastChan ...<-chan []byte) error {
	b.runningMutex.Lock()
	defer b.runningMutex.Unlock()

	if b.isRunning {
		return fmt.Errorf("broadcaster is already running")
	}

	// If broadcastChan was provided as an argument, use it
	if len(broadcastChan) > 0 && broadcastChan[0] != nil {
		b.broadcastChan = broadcastChan[0]
	}

	// Validate that we have a broadcast channel before starting
	if b.broadcastChan == nil {
		return fmt.Errorf("broadcast channel not provided, pass it to either NewBroadcaster or Start")
	}

	b.isRunning = true

	go func() {
		for {
			select {
			case client := <-b.registerChan:
				b.clientsMutex.Lock()
				b.clients[client] = true
				b.clientsMutex.Unlock()
				log.Printf("Client %s connected, total clients: %d", client.ID, len(b.clients))

			case client := <-b.unregisterChan:
				b.clientsMutex.Lock()
				if _, ok := b.clients[client]; ok {
					delete(b.clients, client)
					close(client.Events)
				}
				b.clientsMutex.Unlock()
				log.Printf("Client %s disconnected, total clients: %d", client.ID, len(b.clients))

			case event, ok := <-b.broadcastChan:
				if !ok {
					// Input channel was closed, shutdown the broadcaster
					log.Printf("Broadcast channel closed, shutting down broadcaster")
					b.Stop()
					return
				}

				// Distribute the event to all connected clients
				b.clientsMutex.RLock()
				for client := range b.clients {
					// Non-blocking send to each client's channel
					select {
					case client.Events <- event:
						// Event successfully sent to this client's buffer
					default:
						// Client's buffer is full (client is too slow)
						log.Printf("Dropping event for slow client %s", client.ID)
					}
				}
				b.clientsMutex.RUnlock()

			case <-b.shutdown:
				b.runningMutex.Lock()
				b.isRunning = false
				b.runningMutex.Unlock()

				b.clientsMutex.Lock()
				for client := range b.clients {
					delete(b.clients, client)
					close(client.Events)
				}
				b.clientsMutex.Unlock()
				return
			}
		}
	}()

	return nil
}

// Stop shuts down the broadcaster
func (b *Broadcaster) Stop() {
	b.runningMutex.Lock()
	if b.isRunning {
		close(b.shutdown)
		b.isRunning = false
	}
	b.runningMutex.Unlock()
}

// Register adds a new client to the broadcaster
func (b *Broadcaster) Register(client *Client) {
	b.registerChan <- client
}

// Unregister removes a client from the broadcaster
func (b *Broadcaster) Unregister(client *Client) {
	b.unregisterChan <- client
}

// ClientCount returns the current number of connected clients
func (b *Broadcaster) ClientCount() int {
	b.clientsMutex.RLock()
	defer b.clientsMutex.RUnlock()
	return len(b.clients)
}

// IsRunning returns whether the broadcaster is currently running
func (b *Broadcaster) IsRunning() bool {
	b.runningMutex.Lock()
	defer b.runningMutex.Unlock()
	return b.isRunning
}

// HandlerFunc returns an HTTP handler function for SSE
func (b *Broadcaster) HandlerFunc() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Flush headers immediately
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		} else {
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}

		// Create a client with a buffered channel
		client := &Client{
			ID:     r.RemoteAddr,
			Events: make(chan []byte, 10), // Buffer for this specific client
		}

		// Register the client
		b.Register(client)

		// Make sure to unregister client when connection is closed
		defer b.Unregister(client)

		// Notify client it's connected (optional)
		fmt.Fprintf(w, "data: %s\n\n", `{"type":"connected"}`)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		// Check if the connection has been closed
		notify := r.Context().Done()
		go func() {
			<-notify
			b.Unregister(client)
		}()

		// Stream events to client
		for {
			select {
			case event, ok := <-client.Events:
				if !ok {
					return
				}
				fmt.Fprintf(w, "data: %s\n\n", event)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			case <-notify:
				return
			}
		}
	}
}
