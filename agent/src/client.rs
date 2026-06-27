use anyhow::{Context, Result};
use futures_util::{Sink, SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio_tungstenite::{connect_async, tungstenite::Message};

use crate::config::AgentConfig;
use crate::protocol::{
    CloseSession, Envelope, SessionExit, SessionStarted, StartSession, Stdin, Stdout,
    PROTOCOL_VERSION,
};
use crate::pty_manager::{PtyManager, StartRequest};
use crate::registry::{SessionRecord, SessionRegistry};

#[derive(Debug, Clone)]
pub struct ClientState {
    pub config: AgentConfig,
    pub registry: SessionRegistry,
}

impl ClientState {
    pub fn agent_hello_json(&self) -> Result<String> {
        let payload = serde_json::json!({
            "device_id": self.config.device_id,
            "credential": self.config.credential,
            "platform": std::env::consts::OS,
            "agent_version": env!("CARGO_PKG_VERSION"),
            "protocol_version": PROTOCOL_VERSION,
            "sessions": self.registry.list()
        });
        Ok(serde_json::json!({
            "type": "agent_hello",
            "payload": payload
        })
        .to_string())
    }
}

#[derive(Debug, Serialize)]
struct RegisterRequest {
    token: String,
    name: String,
    platform: String,
    agent_version: String,
    fingerprint: String,
}

#[derive(Debug, Deserialize)]
struct RegisterResponse {
    device_id: String,
    credential: String,
}

pub async fn register_agent(server_url: &str, token: &str, device_name: &str) -> Result<AgentConfig> {
    let request = RegisterRequest {
        token: token.to_string(),
        name: device_name.to_string(),
        platform: std::env::consts::OS.to_string(),
        agent_version: env!("CARGO_PKG_VERSION").to_string(),
        fingerprint: device_fingerprint(device_name),
    };
    let endpoint = format!("{}/api/agents/register", server_url.trim_end_matches('/'));
    let response = reqwest::Client::new()
        .post(endpoint)
        .json(&request)
        .send()
        .await
        .context("send registration request")?;
    if !response.status().is_success() {
        anyhow::bail!("registration failed with status {}", response.status());
    }
    let body: RegisterResponse = response.json().await.context("decode registration response")?;
    Ok(AgentConfig {
        server_url: server_url.to_string(),
        device_id: body.device_id,
        credential: body.credential,
        device_name: device_name.to_string(),
    })
}

pub async fn run_control_loop(config: AgentConfig, mut registry: SessionRegistry) -> Result<()> {
    let ws_url = agent_ws_url(&config.server_url);
    let state = ClientState {
        config: config.clone(),
        registry: registry.clone(),
    };
    let (socket, _) = connect_async(ws_url).await.context("connect agent websocket")?;
    let (mut write, mut read) = socket.split();
    write
        .send(Message::Text(state.agent_hello_json()?))
        .await
        .context("send agent hello")?;

    let mut pty = PtyManager::new();
    let mut buffers = std::collections::BTreeMap::new();

    while let Some(message) = read.next().await {
        let message = message.context("read websocket message")?;
        if !message.is_text() {
            continue;
        }
        let text = message.into_text().context("read text message")?;
        let envelope: Envelope<Value> = serde_json::from_str(&text).context("decode envelope")?;
        match envelope.message_type.as_str() {
            "start_session" => {
                let payload: StartSession = serde_json::from_value(envelope.payload)?;
                let session_id = payload.session_id.clone();
                let started = pty.start(StartRequest {
                    session_id: session_id.clone(),
                    shell_path: payload.shell_path.clone(),
                    working_directory: payload.working_directory.clone(),
                    cols: payload.cols,
                    rows: payload.rows,
                })?;
                registry.upsert(SessionRecord {
                    session_id: session_id.clone(),
                    title: "shell".to_string(),
                    shell_path: payload.shell_path,
                    working_directory: payload.working_directory,
                    status: "running".to_string(),
                    agent_pid: started.pid,
                    last_output_seq: 0,
                });
                buffers.insert(session_id.clone(), crate::buffer::OutputBuffer::new(1024));
                send_payload(
                    &mut write,
                    "session_started",
                    Some(&session_id),
                    SessionStarted {
                        session_id: session_id.clone(),
                        agent_pid: started.pid,
                        title: "shell".to_string(),
                        last_output_seq: 0,
                    },
                )
                .await?;
            }
            "stdin" => {
                let payload: Stdin = serde_json::from_value(envelope.payload)?;
                let session_id = payload.session_id.clone();
                pty.write(&session_id, &payload.data)?;
                let output = pty.read_available(&session_id)?;
                if !output.is_empty() {
                    let buffer = buffers
                        .entry(session_id.clone())
                        .or_insert_with(|| crate::buffer::OutputBuffer::new(1024));
                    let frame = buffer.push(output);
                    send_payload(
                        &mut write,
                        "stdout",
                        Some(&session_id),
                        Stdout {
                            session_id: session_id.clone(),
                            seq: frame.seq,
                            data: frame.data,
                        },
                    )
                    .await?;
                }
            }
            "close_session" => {
                let payload: CloseSession = serde_json::from_value(envelope.payload)?;
                let session_id = payload.session_id.clone();
                pty.close(&session_id)?;
                send_payload(
                    &mut write,
                    "session_exit",
                    Some(&session_id),
                    SessionExit {
                        session_id: session_id.clone(),
                        exit_code: 0,
                        message: "closed".to_string(),
                    },
                )
                .await?;
            }
            _ => {}
        }
    }
    Ok(())
}

fn device_fingerprint(device_name: &str) -> String {
    format!("{}:{}:{}", std::env::consts::OS, std::env::consts::ARCH, device_name)
}

fn agent_ws_url(server_url: &str) -> String {
    let base = server_url.trim_end_matches('/');
    if let Some(rest) = base.strip_prefix("https://") {
        return format!("wss://{rest}/ws/agent");
    }
    if let Some(rest) = base.strip_prefix("http://") {
        return format!("ws://{rest}/ws/agent");
    }
    format!("{base}/ws/agent")
}

async fn send_payload<S, T>(write: &mut S, message_type: &str, session_id: Option<&str>, payload: T) -> Result<()>
where
    S: Sink<Message> + Unpin,
    S::Error: std::error::Error + Send + Sync + 'static,
    T: Serialize,
{
    let envelope = serde_json::json!({
        "type": message_type,
        "session_id": session_id,
        "payload": payload,
    });
    write
        .send(Message::Text(envelope.to_string()))
        .await
        .context("send websocket payload")
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn agent_ws_url_uses_websocket_scheme() {
        assert_eq!(agent_ws_url("https://terminal.example.com"), "wss://terminal.example.com/ws/agent");
        assert_eq!(agent_ws_url("http://localhost:8080/"), "ws://localhost:8080/ws/agent");
    }
}
