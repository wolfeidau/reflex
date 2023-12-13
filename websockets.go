package main

import (
	"errors"
	"io"
	"log"
	"net/http"
	"sync"

	"golang.org/x/net/websocket"
)

type BroadCaster struct {
	mu      *sync.Mutex // protects clients
	clients map[*websocket.Conn]bool
	// Inbound messages from the connections.
	broadcast chan string
}

func NewBroadCaster() *BroadCaster {
	return &BroadCaster{
		broadcast: make(chan string),
		clients:   make(map[*websocket.Conn]bool),
	}
}

func (bc *BroadCaster) Run() {
	for {
		msg := <-bc.broadcast
		for client := range bc.clients {
			_, err := client.Write([]byte(msg))
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("Error: %s", err)
				}

				_ = client.Close()
				bc.mu.Lock()
				delete(bc.clients, client)
				bc.mu.Unlock()
			}
		}
	}
}

func (bc *BroadCaster) Broadcast(in <-chan string) {
	for msg := range in {
		bc.broadcast <- msg
	}
}

func (bc *BroadCaster) Register(client *websocket.Conn) {
	log.Println("connected:", client.RemoteAddr())
	bc.clients[client] = true

	defer client.Close()
	for {
		// wait for a new message
		msg := ""
		err := websocket.Message.Receive(client, &msg)
		if err != nil {
			log.Printf("Receive Error: %s", err)
			bc.mu.Lock()
			delete(bc.clients, client)
			bc.mu.Unlock()
			return
		}
	}
}

func (bc *BroadCaster) StartServer(listenerAddr string) {
	http.Handle("/ws", websocket.Handler(bc.Register))
	err := http.ListenAndServe(listenerAddr, nil)
	if err != nil {
		log.Fatal("StartServer: " + err.Error())
	}
}
