//! Diamond Wasm Transport - High-performance TLS encryption module
//! Provides client-side TLS encryption for proxied HTTP requests

use wasm_bindgen::prelude::*;
use js_sys::Uint8Array;
use rustls::{ClientConfig, ClientConnection, StreamOwned};
use webpki_roots::TLS_SERVER_ROOTS;
use std::sync::Arc;
use std::io::{Read, Write};

#[wasm_bindgen]
pub struct TlsTransport {
    conn: ClientConnection,
    buffer: Vec<u8>,
}

#[wasm_bindgen]
impl TlsTransport {
    #[wasm_bindgen(constructor)]
    pub fn new(hostname: String) -> Result<TlsTransport, JsValue> {
        let mut root_store = rustls::RootCertStore::empty();
        root_store.add_trust_anchors(TLS_SERVER_ROOTS.iter().map(|ta| {
            rustls::OwnedTrustAnchor::from_subject_spki_name_constraints(
                ta.subject,
                ta.spki,
                ta.name_constraints,
            )
        }));

        let config = ClientConfig::builder()
            .with_safe_defaults()
            .with_root_certificates(root_store)
            .with_no_client_auth();

        let server_name = hostname
            .try_into()
            .map_err(|_| JsValue::from_str("Invalid hostname"))?;

        let conn = ClientConnection::new(Arc::new(config), server_name)
            .map_err(|e| JsValue::from_str(&format!("TLS connection error: {}", e)))?;

        Ok(TlsTransport {
            conn,
            buffer: Vec::new(),
        })
    }

    pub fn process_request(&mut self, request_data: Uint8Array) -> Result<Uint8Array, JsValue> {
        let request_bytes = request_data.to_vec();
        
        self.conn
            .write_all(&request_bytes)
            .map_err(|e| JsValue::from_str(&format!("Write error: {}", e)))?;

        self.conn
            .send_tls_record()
            .map_err(|e| JsValue::from_str(&format!("TLS record error: {}", e)))?;

        let mut encrypted = Vec::new();
        self.conn
            .write_tls(&mut encrypted)
            .map_err(|e| JsValue::from_str(&format!("Write TLS error: {}", e)))?;

        Ok(Uint8Array::from(encrypted.as_slice()))
    }

    pub fn decrypt_response(&mut self, encrypted_data: Uint8Array) -> Result<Uint8Array, JsValue> {
        let encrypted_bytes = encrypted_data.to_vec();
        let mut cursor = std::io::Cursor::new(encrypted_bytes);
        
        let bytes_read = self
            .conn
            .read_tls(&mut cursor)
            .map_err(|e| JsValue::from_str(&format!("Read TLS error: {}", e)))?;
        
        if bytes_read == 0 {
            return Ok(Uint8Array::new(0));
        }

        self.conn
            .process_new_packets()
            .map_err(|e| JsValue::from_str(&format!("Process packets error: {}", e)))?;

        let mut decrypted = Vec::new();
        self.conn
            .reader()
            .read_to_end(&mut decrypted)
            .map_err(|e| JsValue::from_str(&format!("Read error: {}", e)))?;

        Ok(Uint8Array::from(decrypted.as_slice()))
    }

    pub fn wants_read(&self) -> bool {
        self.conn.wants_read()
    }

    pub fn wants_write(&self) -> bool {
        self.conn.wants_write()
    }
}

#[wasm_bindgen(start)]
pub fn main() {
    console_error_panic_hook::set_once();
}
