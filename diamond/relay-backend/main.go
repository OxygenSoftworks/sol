package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
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
	if r.URL.Path == "/wisp" && r.Header.Get("Upgrade") == "websocket" {
		s.handleWebSocket(w, r)
		return
	}

	http.ServeFile(w, r, "./frontend/index.html")
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := NewWispServer()
	
	http.Handle("/", server)
	http.HandleFunc("/wisp", server.handleWebSocket)

	log.Printf("Diamond Relay Server starting on port %s", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		log.Fatal("ListenAndServe error:", err)
	}
}
