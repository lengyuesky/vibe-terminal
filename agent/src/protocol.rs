use serde::{Deserialize, Serialize};

pub const PROTOCOL_VERSION: &str = "v1";

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Envelope<T> {
    #[serde(rename = "type")]
    pub message_type: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub session_id: Option<String>,
    pub payload: T,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct StartSession {
    pub session_id: String,
    pub shell_path: String,
    pub working_directory: String,
    pub cols: u16,
    pub rows: u16,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionStarted {
    pub session_id: String,
    pub agent_pid: u32,
    pub title: String,
    pub last_output_seq: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Stdin {
    pub session_id: String,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct Stdout {
    pub session_id: String,
    pub seq: i64,
    pub data: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct CloseSession {
    pub session_id: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct SessionExit {
    pub session_id: String,
    pub exit_code: i32,
    pub message: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn start_session_round_trip() {
        let message = Envelope {
            message_type: "start_session".to_string(),
            session_id: Some("sess-1".to_string()),
            payload: StartSession {
                session_id: "sess-1".to_string(),
                shell_path: "/bin/bash".to_string(),
                working_directory: "/home/dev".to_string(),
                cols: 120,
                rows: 32,
            },
        };
        let json = serde_json::to_string(&message).expect("serialize");
        let decoded: Envelope<StartSession> = serde_json::from_str(&json).expect("decode");
        assert_eq!(decoded.payload.session_id, "sess-1");
        assert_eq!(decoded.payload.cols, 120);
    }
}
