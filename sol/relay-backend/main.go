package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	WispFrameTypeConnect = 0x01
	WispFrameTypeData    = 0x02
	WispFrameTypeClose   = 0x03
	WispFrameTypeError   = 0x04
)

type Stream struct {
	id       uint32
	conn     net.Conn
	ws       *websocket.Conn
	writeMux sync.Mutex
	closed   bool
}

type WispServer struct {
	streams      map[uint32]*Stream
	streamsMutex sync.RWMutex
	upgrader     websocket.Upgrader
}

func NewWispServer() *WispServer {
	return &WispServer{
		streams: make(map[uint32]*Stream),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  65536,
			WriteBufferSize: 65536,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *WispServer) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade error: %v", err)
		return
	}

	go s.handleConnection(conn)
}

func (s *WispServer) handleConnection(ws *websocket.Conn) {
	defer ws.Close()

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket read error: %v", err)
			}
			s.cleanupConnection(ws)
			return
		}

		go s.processFrame(ws, message)
	}
}

func (s *WispServer) processFrame(ws *websocket.Conn, data []byte) {
	if len(data) < 5 {
		return
	}

	frameType := data[0]
	streamId := binary.LittleEndian.Uint32(data[1:5])
	payload := data[5:]

	switch frameType {
	case WispFrameTypeConnect:
		s.handleConnect(ws, streamId, payload)
	case WispFrameTypeData:
		s.handleData(ws, streamId, payload)
	case WispFrameTypeClose:
		s.handleClose(ws, streamId)
	}
}

func (s *WispServer) handleConnect(ws *websocket.Conn, streamId uint32, target []byte) {
	targetStr := string(target)
	
	conn, err := net.DialTimeout("tcp", targetStr, 10*time.Second)
	if err != nil {
		s.sendError(ws, streamId, fmt.Sprintf("Connection failed: %v", err))
		return
	}

	stream := &Stream{
		id:   streamId,
		conn: conn,
		ws:   ws,
	}

	s.streamsMutex.Lock()
	s.streams[streamId] = stream
	s.streamsMutex.Unlock()

	go s.readFromTarget(stream)
}

func (s *WispServer) readFromTarget(stream *Stream) {
	buffer := make([]byte, 65536)
	
	for {
		n, err := stream.conn.Read(buffer)
		if err != nil {
			if err != io.EOF {
				log.Printf("Read from target error: %v", err)
			}
			s.sendClose(stream.ws, stream.id)
			stream.conn.Close()
			s.removeStream(stream.id)
			return
		}

		if n > 0 {
			frame := s.encodeFrame(WispFrameTypeData, stream.id, buffer[:n])
			stream.writeMux.Lock()
			err := stream.ws.WriteMessage(websocket.BinaryMessage, frame)
			stream.writeMux.Unlock()
			
			if err != nil {
				log.Printf("Write to WebSocket error: %v", err)
				stream.conn.Close()
				s.removeStream(stream.id)
				return
			}
		}
	}
}

func (s *WispServer) handleData(ws *websocket.Conn, streamId uint32, payload []byte) {
	s.streamsMutex.RLock()
	stream, exists := s.streams[streamId]
	s.streamsMutex.RUnlock()

	if !exists {
		return
	}

	if len(payload) > 0 {
		stream.writeMux.Lock()
		_, err := stream.conn.Write(payload)
		stream.writeMux.Unlock()
		
		if err != nil {
			log.Printf("Write to target error: %v", err)
			s.sendError(ws, streamId, fmt.Sprintf("Write error: %v", err))
			stream.conn.Close()
			s.removeStream(streamId)
		}
	}
}

func (s *WispServer) handleClose(ws *websocket.Conn, streamId uint32) {
	s.streamsMutex.RLock()
	stream, exists := s.streams[streamId]
	s.streamsMutex.RUnlock()

	if exists {
		stream.conn.Close()
		s.removeStream(streamId)
	}
}

func (s *WispServer) sendError(ws *websocket.Conn, streamId uint32, message string) {
	frame := s.encodeFrame(WispFrameTypeError, streamId, []byte(message))
	ws.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *WispServer) sendClose(ws *websocket.Conn, streamId uint32) {
	frame := s.encodeFrame(WispFrameTypeClose, streamId, nil)
	ws.WriteMessage(websocket.BinaryMessage, frame)
}

func (s *WispServer) encodeFrame(frameType uint8, streamId uint32, payload []byte) []byte {
	header := make([]byte, 5)
	header[0] = frameType
	binary.LittleEndian.PutUint32(header[1:5], streamId)
	
	frame := make([]byte, 5+len(payload))
	copy(frame[:5], header)
	copy(frame[5:], payload)
	
	return frame
}

func (s *WispServer) removeStream(streamId uint32) {
	s.streamsMutex.Lock()
	delete(s.streams, streamId)
	s.streamsMutex.Unlock()
}

func (s *WispServer) cleanupConnection(ws *websocket.Conn) {
	s.streamsMutex.Lock()
	for id, stream := range s.streams {
		if stream.ws == ws {
			stream.conn.Close()
			delete(s.streams, id)
		}
	}
	s.streamsMutex.Unlock()
}

func (s *WispServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Handle WebSocket upgrade for /wisp endpoint
	if r.URL.Path == "/wisp" {
		s.handleWebSocket(w, r)
		return
	}

	// Get the directory where the binary is located
	execPath, err := os.Executable()
	if err != nil {
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}
	execDir := filepath.Dir(execPath)
	
	// Determine base path - if running from sol/relay-backend, frontend is in ../frontend
	// If running from repo root after build, frontend is in sol/frontend
	var frontendDir, swDir string
	
	// Check relative to executable
	if _, err := os.Stat(filepath.Join(execDir, "../frontend/index.html")); err == nil {
		frontendDir = filepath.Join(execDir, "../frontend")
		swDir = filepath.Join(execDir, "../service-worker")
	} else if _, err := os.Stat(filepath.Join(execDir, "../../sol/frontend/index.html")); err == nil {
		frontendDir = filepath.Join(execDir, "../../sol/frontend")
		swDir = filepath.Join(execDir, "../../sol/service-worker")
	} else {
		// Fallback to relative paths
		frontendDir = "../frontend"
		swDir = "../service-worker"
	}

	// Handle service worker
	if r.URL.Path == "/sw.js" {
		swPath := filepath.Join(swDir, "sw.js")
		if _, err := os.Stat(swPath); err == nil {
			http.ServeFile(w, r, swPath)
			return
		}
		http.NotFound(w, r)
		return
	}

	// Handle static files (CSS, JS, JSON, images)
	if r.URL.Path == "/" || r.URL.Path == "" {
		indexPath := filepath.Join(frontendDir, "index.html")
		if _, err := os.Stat(indexPath); err == nil {
			http.ServeFile(w, r, indexPath)
			return
		}
		http.NotFound(w, r)
		return
	}

	// Serve static files from frontend directory
	filePath := filepath.Join(frontendDir, r.URL.Path)
	if _, err := os.Stat(filePath); err == nil {
		http.ServeFile(w, r, filePath)
		return
	}

	// For SPA routing, serve index.html for unknown paths
	indexPath := filepath.Join(frontendDir, "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		http.ServeFile(w, r, indexPath)
		return
	}
	
	http.NotFound(w, r)
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := NewWispServer()
	
	http.Handle("/", server)
	http.HandleFunc("/wisp", server.handleWebSocket)

	log.Printf("Sol Relay Server starting on port %s", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe error:", err)
	}
}
