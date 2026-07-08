use anyhow::{Context, Result};
use futures_util::{Sink, SinkExt, StreamExt};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::collections::BTreeMap;
use std::time::Duration;
use tokio::time;
use tokio_tungstenite::{connect_async, tungstenite::Message};

use crate::buffer::OutputBuffer;
use crate::config::AgentConfig;
use crate::protocol::{
    CloseSession, Envelope, Resize, SessionExit, SessionStarted, StartSession, Stdin, Stdout,
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
            "capabilities": ["fs"],
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

pub async fn register_agent(
    server_url: &str,
    token: &str,
    device_name: &str,
) -> Result<AgentConfig> {
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
    let body: RegisterResponse = response
        .json()
        .await
        .context("decode registration response")?;
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
    let (socket, _) = connect_async(ws_url)
        .await
        .context("connect agent websocket")?;
    let (mut write, mut read) = socket.split();
    write
        .send(Message::Text(state.agent_hello_json()?))
        .await
        .context("send agent hello")?;

    let mut pty = PtyManager::new();
    let mut buffers: BTreeMap<String, OutputBuffer> = BTreeMap::new();
    let mut uploads = crate::fs::UploadManager::new();
    let mut output_tick = time::interval(Duration::from_millis(30));

    loop {
        tokio::select! {
            message = read.next() => {
                let Some(message) = message else {
                    return Ok(());
                };
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
                        buffers.insert(session_id.clone(), OutputBuffer::new(1024));
                        send_payload(
                            &mut write,
                            "session_started",
                            None,
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
                        write_stdin_if_session_exists(&mut pty, &buffers, &payload)?;
                    }
                    "resize" => {
                        let payload: Resize = serde_json::from_value(envelope.payload)?;
                        resize_if_session_exists(&mut pty, &buffers, &payload)?;
                    }
                    "close_session" => {
                        let payload: CloseSession = serde_json::from_value(envelope.payload)?;
                        let session_id = payload.session_id.clone();
                        pty.close(&session_id)?;
                        buffers.remove(&session_id);
                        send_payload(
                            &mut write,
                            "session_exit",
                            None,
                            Some(&session_id),
                            SessionExit {
                                session_id: session_id.clone(),
                                exit_code: 0,
                                message: "closed".to_string(),
                            },
                        )
                        .await?;
                    }
                    other => {
                        let request_id = envelope.request_id.clone();
                        if let Some((reply_type, reply_payload)) =
                            crate::fs::handle_fs_message(&mut uploads, other, envelope.payload)
                        {
                            send_payload(
                                &mut write,
                                &reply_type,
                                request_id.as_deref(),
                                None,
                                reply_payload,
                            )
                            .await?;
                        }
                    }
                }
            }
            _ = output_tick.tick() => {
                for frame in flush_pty_outputs(&mut pty, &mut buffers)? {
                    send_payload(
                        &mut write,
                        "stdout",
                        None,
                        Some(&frame.session_id),
                        Stdout {
                            session_id: frame.session_id.clone(),
                            seq: frame.seq,
                            data: frame.data.clone(),
                        },
                    )
                    .await?;
                }
                for session_id in pty.drain_exited() {
                    buffers.remove(&session_id);
                    send_payload(
                        &mut write,
                        "session_exit",
                        None,
                        Some(&session_id),
                        SessionExit {
                            session_id: session_id.clone(),
                            exit_code: 0,
                            message: "exited".to_string(),
                        },
                    )
                    .await?;
                }
                uploads.cleanup_stale(Duration::from_secs(60));
            }
        }
    }
}

fn write_stdin_if_session_exists(
    pty: &mut PtyManager,
    buffers: &BTreeMap<String, OutputBuffer>,
    payload: &Stdin,
) -> Result<()> {
    if !buffers.contains_key(&payload.session_id) {
        return Ok(());
    }
    pty.write(&payload.session_id, &payload.data)
}

fn resize_if_session_exists(
    pty: &mut PtyManager,
    buffers: &BTreeMap<String, OutputBuffer>,
    payload: &Resize,
) -> Result<()> {
    if !buffers.contains_key(&payload.session_id) {
        return Ok(());
    }
    pty.resize(&payload.session_id, payload.cols, payload.rows)
}

#[derive(Debug, Clone, PartialEq)]
struct SessionOutputFrame {
    session_id: String,
    seq: i64,
    data: String,
}

fn flush_pty_outputs(
    pty: &mut PtyManager,
    buffers: &mut BTreeMap<String, OutputBuffer>,
) -> Result<Vec<SessionOutputFrame>> {
    let mut frames = Vec::new();
    for session_id in buffers.keys().cloned().collect::<Vec<_>>() {
        if !pty.has_session(&session_id) {
            continue;
        }
        let output = pty.read_available(&session_id)?;
        if output.is_empty() {
            continue;
        }
        let frame = buffers
            .entry(session_id.clone())
            .or_insert_with(|| OutputBuffer::new(1024))
            .push(output);
        frames.push(SessionOutputFrame {
            session_id,
            seq: frame.seq,
            data: frame.data,
        });
    }
    Ok(frames)
}

fn device_fingerprint(device_name: &str) -> String {
    format!(
        "{}:{}:{}",
        std::env::consts::OS,
        std::env::consts::ARCH,
        device_name
    )
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

async fn send_payload<S, T>(
    write: &mut S,
    message_type: &str,
    request_id: Option<&str>,
    session_id: Option<&str>,
    payload: T,
) -> Result<()>
where
    S: Sink<Message> + Unpin,
    S::Error: std::error::Error + Send + Sync + 'static,
    T: Serialize,
{
    let envelope = serde_json::json!({
        "type": message_type,
        "request_id": request_id,
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
    use std::collections::BTreeMap;
    use std::time::Duration;

    use crate::buffer::OutputBuffer;

    #[test]
    fn agent_ws_url_uses_websocket_scheme() {
        assert_eq!(
            agent_ws_url("https://terminal.example.com"),
            "wss://terminal.example.com/ws/agent"
        );
        assert_eq!(
            agent_ws_url("http://localhost:8080/"),
            "ws://localhost:8080/ws/agent"
        );
    }

    #[test]
    fn flush_pty_outputs_collects_echo_without_another_input() {
        let mut pty = PtyManager::new();
        let session = pty
            .start(StartRequest {
                session_id: "sess-echo".into(),
                shell_path: "/bin/cat".into(),
                working_directory: "/tmp".into(),
                cols: 80,
                rows: 24,
            })
            .expect("start pty");
        let mut buffers = BTreeMap::new();
        buffers.insert(session.session_id.clone(), OutputBuffer::new(16));

        pty.write(&session.session_id, "p").expect("write");
        std::thread::sleep(Duration::from_millis(100));

        let frames = flush_pty_outputs(&mut pty, &mut buffers).expect("flush");
        assert!(
            frames
                .iter()
                .any(|frame| frame.session_id == session.session_id && frame.data.contains('p')),
            "frames were {frames:?}"
        );
    }

    #[test]
    fn agent_hello_declares_fs_capability() {
        let state = ClientState {
            config: AgentConfig {
                server_url: "http://localhost:8080".into(),
                device_id: "dev-1".into(),
                credential: "cred".into(),
                device_name: "test".into(),
            },
            registry: SessionRegistry::default(),
        };
        let hello = state.agent_hello_json().expect("hello");
        let value: serde_json::Value = serde_json::from_str(&hello).expect("json");
        assert_eq!(value["payload"]["capabilities"][0], "fs");
    }

    #[test]
    fn stdin_for_unknown_session_does_not_disconnect_agent() {
        let mut pty = PtyManager::new();
        let buffers = BTreeMap::new();
        let payload = Stdin {
            session_id: "missing".into(),
            data: "pwd\n".into(),
        };

        write_stdin_if_session_exists(&mut pty, &buffers, &payload)
            .expect("unknown stdin should be ignored");
    }

    #[test]
    fn resize_for_unknown_session_does_not_disconnect_agent() {
        let mut pty = PtyManager::new();
        let buffers = BTreeMap::new();
        let payload = Resize {
            session_id: "missing".into(),
            cols: 120,
            rows: 40,
        };

        resize_if_session_exists(&mut pty, &buffers, &payload)
            .expect("unknown resize should be ignored");
    }
}
