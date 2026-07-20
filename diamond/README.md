# Diamond Proxy

High-performance interception-based web proxy using WebAssembly and Wisp protocol.

## Architecture

### 1. WebAssembly Transport (`wasm-transport/`)
Rust-based TLS encryption module compiled to WebAssembly for client-side encryption.

**Build:**
```bash
cd wasm-transport
rustup target add wasm32-unknown-unknown
cargo build --release --target wasm32-unknown-unknown
wasm-bindgen --out-dir ../frontend/wasm target/wasm32-unknown-unknown/release/diamond_transport.wasm
```

### 2. Service Worker (`service-worker/`)
JavaScript Service Worker that intercepts fetch/XHR/WebSocket requests and multiplexes them over Wisp protocol.

### 3. Relay Backend (`relay-backend/`)
Go-based high-throughput Wisp server that demultiplexes streams and routes traffic.

**Build & Run:**
```bash
cd relay-backend
go mod tidy
go build -o diamond-relay
./diamond-relay
```

**Environment Variables:**
- `PORT`: Server port (default: 8080)

### 4. Frontend (`frontend/`)
Vanilla JS/HTML/CSS interface with dynamic app/game loading from JSON files.

## Deployment

### Render
1. Create a new Web Service
2. Set Build Command: `cd relay-backend && go build -o diamond-relay`
3. Set Start Command: `./diamond-relay`
4. Serve frontend static files from the `frontend/` directory

### Railway
1. Create a new project
2. Add `go.mod` and `main.go` from `relay-backend/`
3. Set `PORT` environment variable
4. Deploy static files to a CDN or serve from the Go server

## File Structure

```
diamond/
├── wasm-transport/
│   ├── Cargo.toml
│   └── src/
│       └── lib.rs
├── service-worker/
│   └── sw.js
├── relay-backend/
│   ├── main.go
│   └── go.mod
└── frontend/
    ├── index.html
    ├── styles.css
    ├── app.js
    ├── apps.json
    └── games.json
```

## Protocol

The Wisp protocol uses binary frames:
- Byte 0: Frame type (0x01=Connect, 0x02=Data, 0x03=Close, 0x04=Error)
- Bytes 1-4: Stream ID (little-endian uint32)
- Remaining bytes: Payload data

## License

MIT
