// Diamond Service Worker - Wisp Protocol Multiplexer and Fetch Interceptor

let wasmModule = null;
let wispConnection = null;
const streamRegistry = new Map();
let streamIdCounter = 1;

self.addEventListener('install', (event) => {
    event.waitUntil(
        caches.open('diamond-v1').then((cache) => {
            return cache.addAll(['/index.html', '/sw.js']);
        })
    );
    self.skipWaiting();
});

self.addEventListener('activate', (event) => {
    event.waitUntil(self.clients.claim());
});

async function initWasm() {
    if (!wasmModule) {
        wasmModule = await import('./wasm/diamond_transport.js');
        await wasmModule.default();
    }
    return wasmModule;
}

async function getWispConnection() {
    if (!wispConnection || wispConnection.readyState !== WebSocket.OPEN) {
        const wsUrl = `wss://${self.location.hostname}/wisp`;
        wispConnection = new WebSocket(wsUrl);
        wispConnection.binaryType = 'arraybuffer';
        
        await new Promise((resolve, reject) => {
            wispConnection.onopen = () => resolve();
            wispConnection.onerror = (e) => reject(e);
            wispConnection.onclose = () => {
                wispConnection = null;
            };
        });
    }
    return wispConnection;
}

function allocateStreamId() {
    const id = streamIdCounter++;
    if (streamIdCounter > 0xFFFFFFFF) streamIdCounter = 1;
    return id;
}

function encodeWispFrame(streamId, type, data) {
    const header = new ArrayBuffer(5);
    const view = new DataView(header);
    view.setUint8(0, type);
    view.setUint32(1, streamId, true);
    
    const dataArray = data ? new Uint8Array(data) : new Uint8Array(0);
    const frame = new Uint8Array(header.byteLength + dataArray.byteLength);
    frame.set(new Uint8Array(header), 0);
    frame.set(dataArray, header.byteLength);
    
    return frame.buffer;
}

function decodeWispFrame(buffer) {
    const view = new DataView(buffer);
    const type = view.getUint8(0);
    const streamId = view.getUint32(1, true);
    const data = new Uint8Array(buffer, 5);
    return { type, streamId, data };
}

async function handleFetchRequest(request, clientId) {
    const wasm = await initWasm();
    const url = new URL(request.url);
    const hostname = url.hostname;
    
    try {
        const transport = new wasm.TlsTransport(hostname);
        
        let method = request.method;
        let headers = '';
        request.headers.forEach((value, key) => {
            headers += `${key}: ${value}\r\n`;
        });
        
        const body = request.body ? await request.arrayBuffer() : null;
        const bodyLength = body ? body.byteLength : 0;
        
        let httpRequest = `${method} ${url.pathname}${url.search} HTTP/1.1\r\n`;
        httpRequest += `Host: ${hostname}\r\n`;
        httpRequest += headers;
        httpRequest += `Content-Length: ${bodyLength}\r\n`;
        httpRequest += '\r\n';
        
        const requestBytes = new Uint8Array([...new TextEncoder().encode(httpRequest), ...(body ? new Uint8Array(body) : [])]);
        const encryptedRequest = transport.process_request(requestBytes);
        
        const wisp = await getWispConnection();
        const streamId = allocateStreamId();
        
        const connectFrame = encodeWispFrame(streamId, 0x01, new TextEncoder().encode(`${hostname}:443`));
        wisp.send(connectFrame);
        
        const dataFrame = encodeWispFrame(streamId, 0x02, encryptedRequest);
        wisp.send(dataFrame);
        
        return new Promise((resolve) => {
            const chunks = [];
            let responseComplete = false;
            
            const messageHandler = (event) => {
                const frame = decodeWispFrame(event.data);
                if (frame.streamId !== streamId) return;
                
                if (frame.type === 0x02) {
                    chunks.push(frame.data);
                    
                    if (responseComplete) {
                        wisp.removeEventListener('message', messageHandler);
                        streamRegistry.delete(streamId);
                        
                        const responseData = new Uint8Array(chunks.reduce((acc, chunk) => acc + chunk.length, 0));
                        let offset = 0;
                        chunks.forEach(chunk => {
                            responseData.set(chunk, offset);
                            offset += chunk.length;
                        });
                        
                        const decrypted = transport.decrypt_response(responseData);
                        const responseText = new TextDecoder().decode(decrypted);
                        
                        const lines = responseText.split('\r\n\r\n');
                        const headerLines = lines[0].split('\r\n');
                        const statusLine = headerLines[0];
                        const statusMatch = statusLine.match(/HTTP\/\d\.\d (\d+)/);
                        const statusCode = statusMatch ? parseInt(statusMatch[1]) : 200;
                        
                        const headers = new Headers();
                        headerLines.slice(1).forEach(line => {
                            const [key, value] = line.split(': ');
                            if (key && value) headers.append(key, value);
                        });
                        
                        const bodyData = lines.slice(1).join('\r\n\r\n');
                        const bodyBuffer = new TextEncoder().encode(bodyData);
                        
                        resolve(new Response(bodyBuffer, {
                            status: statusCode,
                            headers: headers
                        }));
                    }
                } else if (frame.type === 0x03) {
                    responseComplete = true;
                }
            };
            
            wisp.addEventListener('message', messageHandler);
            streamRegistry.set(streamId, { resolve, transport, chunks });
        });
    } catch (error) {
        console.error('Proxy error:', error);
        return fetch(request);
    }
}

self.addEventListener('fetch', (event) => {
    const url = new URL(event.request.url);
    
    if (url.hostname === self.location.hostname) {
        return;
    }
    
    event.respondWith(handleFetchRequest(event.request, event.clientId));
});

self.addEventListener('message', (event) => {
    if (event.data.type === 'PROXY_REQUEST') {
        handleFetchRequest(event.data.request, event.source.id)
            .then(response => response.arrayBuffer())
            .then(buffer => {
                event.ports[0].postMessage({ buffer });
            });
    }
});

class WispClient {
    constructor(wsUrl) {
        this.wsUrl = wsUrl;
        this.ws = null;
        this.streams = new Map();
        this.reconnectDelay = 1000;
    }
    
    async connect() {
        return new Promise((resolve, reject) => {
            this.ws = new WebSocket(this.wsUrl);
            this.ws.binaryType = 'arraybuffer';
            
            this.ws.onopen = () => {
                this.reconnectDelay = 1000;
                resolve();
            };
            
            this.ws.onerror = (e) => {
                reject(e);
            };
            
            this.ws.onmessage = (event) => this.handleMessage(event.data);
            this.ws.onclose = () => {
                setTimeout(() => this.connect(), this.reconnectDelay);
                this.reconnectDelay = Math.min(this.reconnectDelay * 2, 30000);
            };
        });
    }
    
    handleMessage(data) {
        const frame = decodeWispFrame(data);
        const stream = this.streams.get(frame.streamId);
        
        if (stream) {
            if (frame.type === 0x02 && stream.onData) {
                stream.onData(frame.data);
            } else if (frame.type === 0x03 && stream.onClose) {
                stream.onClose();
                this.streams.delete(frame.streamId);
            } else if (frame.type === 0x04 && stream.onError) {
                stream.onError(new Error('Stream error'));
            }
        }
    }
    
    createStream(onData, onClose, onError) {
        const streamId = allocateStreamId();
        this.streams.set(streamId, { onData, onClose, onError });
        return streamId;
    }
    
    sendConnect(streamId, target) {
        const frame = encodeWispFrame(streamId, 0x01, new TextEncoder().encode(target));
        this.ws.send(frame);
    }
    
    sendData(streamId, data) {
        const frame = encodeWispFrame(streamId, 0x02, data);
        this.ws.send(frame);
    }
    
    closeStream(streamId) {
        const frame = encodeWispFrame(streamId, 0x03, null);
        this.ws.send(frame);
        this.streams.delete(streamId);
    }
}
